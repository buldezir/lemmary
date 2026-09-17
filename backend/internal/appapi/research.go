package appapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
)

// Research is the surface that keeps its conversation. Where a search turn is
// one completion answered and forgotten, a research turn is a thread: the
// question, every tool call it made, everything the tools returned, and the
// answer, all appended as they happen and all replayed on the next question.
//
// That is what this file owns and search.go does not. The two shared nothing
// but a handler once, and the handler was where the differences collected.

// threadRecorder appends a research run's messages as the run produces them.
// A row on disk before the answer exists is the whole point: a run that dies
// has still bought the documents it read, and the next turn inherits them.
//
// A failed append is logged and swallowed. Losing one row costs the next turn
// some context; failing the run would cost the answer the provider was paid
// for.
type threadRecorder struct {
	app  core.App
	turn searchTurn
	// pending holds the trail lines finished since the last row was written.
	// They ride on the row they describe -- the tool result, or the answer at
	// the end -- which is what lets a turn whose run died still show what it
	// got through, with no second place the trail is derived from.
	pending []chat.StoredStep
}

func (r *threadRecorder) step(event ai.ResearchEvent) {
	if event.Type == "step" && event.Status == "done" {
		r.pending = append(r.pending, chat.StepFromEvent(event))
	}
}

// drain hands over the trail so far and forgets it.
func (r *threadRecorder) drain() []chat.StoredStep {
	steps := r.pending
	r.pending = nil
	return steps
}

func (r *threadRecorder) record(msg ai.ThreadMessage) {
	entry := chat.ThreadEntry{
		Role:    msg.Role,
		Content: msg.Content,
		RunID:   r.turn.runID,
		Calls:   msg.Calls,
		CallID:  msg.CallID,
		Mode:    r.turn.mode,
	}
	if msg.Role == chat.RoleTool {
		entry.Steps = r.drain()
	}
	if _, err := chat.AppendThreadMessage(r.app, r.turn.ownerID, r.turn.session.Id, entry); err != nil {
		r.app.Logger().Error("research thread append failed",
			"session", r.turn.session.Id, "role", msg.Role, slog.Any("error", err))
	}
}

// runResearchTurn stores the question, replays the conversation and runs the
// loop, appending every message it produces. Shared by the streaming endpoint
// and the non-streaming fallback, which differ only in who is listening.
func runResearchTurn(app core.App, turn searchTurn, ctx context.Context, recorder *threadRecorder, emit func(ai.ResearchEvent)) (ai.ResearchResult, error) {
	if emit == nil {
		emit = func(ai.ResearchEvent) {}
	}

	request := ai.ResearchRequest{
		AvailableTags:  turn.tools.tags,
		DenseRetrieval: turn.tools.dense,
		Web:            turn.tools.web,
	}

	stored, err := chat.Thread(app, turn.session.Id)
	if err != nil {
		return ai.ResearchResult{}, fmt.Errorf("read the stored conversation: %w", err)
	}
	// Written before the question, so the stored order is the order it replays
	// in, and written once: a conversation keeps the prompt it opened with, or
	// a tag added between two questions would move the prefix the provider has
	// cached.
	if len(stored) == 0 {
		recorder.record(ai.ThreadMessage{Role: chat.RoleSystem, Content: turn.agent.SystemPrompt(request)})
	}

	// The question is stored before the first provider call, not after the last.
	// A run that never answers then leaves a conversation that says what was
	// asked and how far it got, instead of nothing at all.
	if !turn.resume {
		recorder.record(ai.ThreadMessage{Role: chat.RoleUser, Content: turn.content})
	}

	thread, err := chat.Thread(app, turn.session.Id)
	if err != nil {
		return ai.ResearchResult{}, fmt.Errorf("read the stored conversation: %w", err)
	}

	return turn.agent.Research(ctx, ai.ResearchRequest{
		Thread:         thread,
		Record:         recorder.record,
		AvailableTags:  turn.tools.tags,
		Search:         turn.tools.search,
		Read:           turn.tools.read,
		PriorDocuments: turn.priorDocuments,
		DenseRetrieval: turn.tools.dense,
		Survey:         turn.tools.survey,
		Count:          turn.tools.count,
		Find:           turn.tools.find,
		Web:            turn.tools.web,
		ContextWindow:  turn.contextWindow,
	}, func(event ai.ResearchEvent) {
		recorder.step(event)
		emit(event)
	})
}

// streamResearchTurn runs the loop with the client watching.
func streamResearchTurn(app core.App, turn searchTurn, ctx context.Context, stream *sseWriter) error {
	recorder := &threadRecorder{app: app, turn: turn}
	result, err := runResearchTurn(app, turn, ctx, recorder, func(event ai.ResearchEvent) { stream.Send(event) })
	if err != nil {
		// Nothing is taken back. Whatever the run got through is on disk, the
		// turn reads as unfinished, and the next question -- or a resume --
		// continues from there.
		if runErr := ctx.Err(); runErr != nil {
			app.Logger().Info("research run stopped", slog.Any("error", runErr))
			if errors.Is(runErr, context.DeadlineExceeded) {
				stream.Send(ai.ResearchEvent{Type: "error", Message: runTooLongMessage})
			}
			stream.Send(ai.ResearchEvent{Type: "done"})
			return nil
		}
		app.Logger().Error("research run failed", slog.Any("error", err))
		stream.Send(ai.ResearchEvent{Type: "error", Message: ai.ProviderErrorMessage(err)})
		stream.Send(ai.ResearchEvent{Type: "done"})
		return nil
	}

	documents := result.Documents
	if documents == nil {
		documents = []ai.DocumentHit{}
	}

	// Stored before anything is written to the socket, and unconditionally: a
	// write to a half-closed connection can block until the kernel gives up,
	// and that must never sit between a finished answer and the save.
	saved := persistResearchAnswer(app, turn, result, documents, recorder.drain())

	stream.Send(ai.ResearchEvent{Type: "documents", Documents: documents})
	stream.Send(ai.ResearchEvent{
		Type:       "message",
		Content:    result.Reply,
		Incomplete: result.Incomplete,
	})
	stream.Send(searchSavedEvent{
		Type:      "saved",
		Session:   saved.Session,
		Message:   saved.Message,
		Documents: saved.Documents,
		Saved:     saved.Saved,
		Detail:    saved.Detail,
	})
	stream.Send(ai.ResearchEvent{Type: "done"})
	return nil
}

// persistResearchAnswer writes the row the turn was for. The rest of the thread
// is already on disk; this is the one that makes the turn finished, and it
// carries what belongs to the answer rather than to the work behind it.
func persistResearchAnswer(app core.App, t searchTurn, result ai.ResearchResult, documents []ai.DocumentHit, steps []chat.StoredStep) searchResponse {
	session, err := chat.AppendThreadMessage(app, t.ownerID, t.session.Id, chat.ThreadEntry{
		Role:       chat.RoleAssistant,
		Content:    result.Reply,
		RunID:      t.runID,
		Documents:  documents,
		Steps:      steps,
		Usage:      result.Usage,
		Incomplete: result.Incomplete,
		Mode:       t.mode,
	})
	if err != nil {
		app.Logger().Error("research answer persist failed", slog.Any("error", err))
		return searchResponse{
			Message:   unsavedMessage(chat.RoleAssistant, result.Reply, documents),
			Documents: documents,
			Saved:     false,
			// The work is not lost, only this answer, and saying so is the
			// difference between "ask again" and "start over".
			Detail: "This answer could not be saved. The research behind it was, so asking again will not repeat it.",
		}
	}

	info := chat.ToSessionInfo(session)
	return searchResponse{
		Session:   &info,
		Message:   latestAssistantMessage(app, session.Id, t.runID, result.Reply, documents),
		Documents: documents,
		Saved:     true,
	}
}
