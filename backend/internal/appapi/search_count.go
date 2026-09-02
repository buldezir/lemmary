package appapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"

	"lemmary/backend/internal/ai"
)

// maxCountIDs is the most matching documents a grouped count over a text
// query enumerates before the breakdown is declared approximate. The plain
// count is exact whatever the size: the index reports it without fetching.
const maxCountIDs = 5000

// maxCountGroups is how many groups a breakdown lists; the rest are summed
// into Other.
const maxCountGroups = 50

// dbProvider is the one extra thing counting needs from the app: a query
// builder over the documents table. Optional on the retriever's app so the
// stubs that test search and read need not grow a database.
type dbProvider interface {
	DB() dbx.Builder
}

// count backs the agent's count_documents tool.
//
// Two sources, by what the call asks. Filters alone are a database question:
// COUNT(*) with the same owner, type, correspondent, tag and date predicates,
// GROUP BY when asked. Query text is the index's: it knows which documents
// contain the words, and reports the exact total without fetching a hit. A
// grouped count over text takes the index's ids to the database, which is the
// one place the two can disagree -- a document indexed but since deleted -- and
// the one place a very large match set is cut, marked approximate.
func (r *agentRetriever) count(ctx context.Context, args ai.CountArgs) (ai.CountResult, error) {
	db, ok := r.app.(dbProvider)
	if !ok {
		return ai.CountResult{}, fmt.Errorf("counting is unavailable")
	}
	groupBy := strings.ToLower(strings.TrimSpace(args.GroupBy))

	ftQuery, unresolved, err := r.resolveFilters(args.SearchArgs())
	if err != nil {
		return ai.CountResult{}, err
	}
	if len(unresolved) > 0 {
		return ai.CountResult{Count: 0, GroupBy: groupBy, Unresolved: unresolved}, nil
	}

	spec := countSpec{
		userID:           r.userID,
		documentTypeIDs:  ftQuery.DocumentTypeIDs,
		correspondentIDs: ftQuery.CorrespondentIDs,
		tagIDs:           ftQuery.TagIDs,
		dateFrom:         ftQuery.DateFrom,
		dateTo:           ftQuery.DateTo,
		groupBy:          groupBy,
	}

	result := ai.CountResult{GroupBy: groupBy}
	if query := strings.TrimSpace(args.Query); query != "" {
		if r.idx == nil || !r.idx.Ready() {
			return ai.CountResult{}, fmt.Errorf("search index is not ready")
		}
		if groupBy == "" {
			total, err := r.idx.CountMatching(ftQuery)
			if err != nil {
				return ai.CountResult{}, fmt.Errorf("count documents: %w", err)
			}
			result.Count = int(total)
			return result, nil
		}
		ids, total, complete, err := r.idx.MatchingIDs(ftQuery, maxCountIDs)
		if err != nil {
			return ai.CountResult{}, fmt.Errorf("count documents: %w", err)
		}
		result.Count = int(total)
		result.Approximate = !complete
		if len(ids) == 0 {
			return result, nil
		}
		spec.ids = ids
		// The predicates are already satisfied by every id the index
		// returned; only the owner check is kept, as the boundary that has
		// to hold whatever the index says.
		spec.documentTypeIDs, spec.correspondentIDs, spec.tagIDs = nil, nil, nil
		spec.dateFrom, spec.dateTo = "", ""
	}

	rows, total, err := countDocuments(ctx, db.DB(), spec)
	if err != nil {
		return ai.CountResult{}, fmt.Errorf("count documents: %w", err)
	}
	if spec.ids == nil {
		result.Count = total
	}
	if groupBy != "" {
		result.Groups, result.Other = r.nameGroups(groupBy, rows)
	}
	return result, nil
}

// countSpec is a count as SQL sees it: predicates, an optional id set, and
// what to group by.
type countSpec struct {
	userID           string
	documentTypeIDs  []string
	correspondentIDs []string
	tagIDs           []string
	dateFrom, dateTo string
	groupBy          string
	// ids, when set, restricts the count to these documents.
	ids []string
}

type countRow struct {
	Key   string `db:"key"`
	Count int    `db:"count"`
}

// countDocuments runs the count. It takes the builder rather than the app so
// it can be tested against a bare SQLite file with a hand-made documents
// table. Returns the grouped rows (one row with an empty key when not
// grouping) and the total.
//
// Dates are compared on their first ten characters: a DateField column is
// TEXT and holds both "YYYY-MM-DD" and "YYYY-MM-DD HH:MM:SS.sssZ", and an
// empty date is excluded from any range rather than sorting below every
// bound. Tags are a JSON array of ids in a text column; json_valid guards the
// legacy empty string, which json_each would abort the whole query on.
func countDocuments(ctx context.Context, db dbx.Builder, spec countSpec) ([]countRow, int, error) {
	if db == nil {
		return nil, 0, fmt.Errorf("no database")
	}
	where := []string{}
	params := dbx.Params{}
	if spec.userID != "" {
		where = append(where, `d.user = {:user}`)
		params["user"] = spec.userID
	}
	if in := inClause("dt", spec.documentTypeIDs, params); in != "" {
		where = append(where, `d.document_type IN `+in)
	}
	if in := inClause("co", spec.correspondentIDs, params); in != "" {
		where = append(where, `d.correspondent IN `+in)
	}
	if in := inClause("tg", spec.tagIDs, params); in != "" {
		where = append(where, `(json_valid(d.tags) AND EXISTS (SELECT 1 FROM json_each(d.tags) t WHERE t.value IN `+in+`))`)
	}
	if spec.dateFrom != "" || spec.dateTo != "" {
		where = append(where, `COALESCE(d.document_date, '') != ''`)
		if spec.dateFrom != "" {
			where = append(where, `substr(d.document_date, 1, 10) >= {:date_from}`)
			params["date_from"] = spec.dateFrom
		}
		if spec.dateTo != "" {
			where = append(where, `substr(d.document_date, 1, 10) <= {:date_to}`)
			params["date_to"] = spec.dateTo
		}
	}

	key := `''`
	from := `documents d`
	switch spec.groupBy {
	case "document_type":
		key = `COALESCE(d.document_type, '')`
	case "correspondent":
		key = `COALESCE(d.correspondent, '')`
	case "year":
		key = `substr(COALESCE(d.document_date, ''), 1, 4)`
	case "month":
		key = `substr(COALESCE(d.document_date, ''), 1, 7)`
	case "tag":
		// One row per (document, tag); a document with no tags counts
		// under the empty key so the total still adds up.
		from = `documents d LEFT JOIN json_each(CASE WHEN json_valid(d.tags) THEN d.tags ELSE '[]' END) t`
		key = `COALESCE(t.value, '')`
	case "":
	default:
		return nil, 0, fmt.Errorf("unknown group_by %q", spec.groupBy)
	}

	var rows []countRow
	total := 0
	run := func(ids []string) error {
		conds := append([]string{}, where...)
		p := dbx.Params{}
		for k, v := range params {
			p[k] = v
		}
		if ids != nil {
			conds = append(conds, `d.id IN `+inClause("id", ids, p))
		}
		sql := `SELECT ` + key + ` AS key, COUNT(*) AS count FROM ` + from
		if len(conds) > 0 {
			sql += ` WHERE ` + strings.Join(conds, " AND ")
		}
		// By position, not by name: json_each has a column called key of its
		// own, and GROUP BY key would take that -- the array index -- over the
		// alias.
		sql += ` GROUP BY 1`
		var got []countRow
		if err := db.NewQuery(sql).Bind(p).WithContext(ctx).All(&got); err != nil {
			return err
		}
		rows = append(rows, got...)
		if spec.groupBy == "tag" {
			// Documents, not (document, tag) pairs.
			countSQL := `SELECT COUNT(*) AS count FROM documents d`
			if len(conds) > 0 {
				countSQL += ` WHERE ` + strings.Join(conds, " AND ")
			}
			var n struct {
				Count int `db:"count"`
			}
			if err := db.NewQuery(countSQL).Bind(p).WithContext(ctx).One(&n); err != nil {
				return err
			}
			total += n.Count
		} else {
			for _, row := range got {
				total += row.Count
			}
		}
		return nil
	}

	if spec.ids == nil {
		if err := run(nil); err != nil {
			return nil, 0, err
		}
	} else {
		// SQLite's parameter limit is generous but not unbounded; chunk.
		const chunk = 500
		for start := 0; start < len(spec.ids); start += chunk {
			end := min(start+chunk, len(spec.ids))
			if err := run(spec.ids[start:end]); err != nil {
				return nil, 0, err
			}
		}
		rows = mergeCountRows(rows)
	}
	return rows, total, nil
}

// inClause renders a bound IN list for ids, registering the parameters under
// prefix. Empty for no ids.
func inClause(prefix string, ids []string, params dbx.Params) string {
	if len(ids) == 0 {
		return ""
	}
	names := make([]string, 0, len(ids))
	for i, id := range ids {
		name := fmt.Sprintf("%s%d", prefix, i)
		params[name] = id
		names = append(names, "{:"+name+"}")
	}
	return "(" + strings.Join(names, ", ") + ")"
}

func mergeCountRows(rows []countRow) []countRow {
	byKey := map[string]int{}
	for _, row := range rows {
		byKey[row.Key] += row.Count
	}
	out := make([]countRow, 0, len(byKey))
	for k, n := range byKey {
		out = append(out, countRow{Key: k, Count: n})
	}
	return out
}

// nameGroups turns id keys into names, sorts by count, and folds the tail
// into Other. An empty key is the documents without the property.
func (r *agentRetriever) nameGroups(groupBy string, rows []countRow) ([]ai.CountGroup, int) {
	groups := make([]ai.CountGroup, 0, len(rows))
	for _, row := range rows {
		key := row.Key
		switch groupBy {
		case "document_type":
			if key != "" {
				key = strings.TrimSpace(relatedName(r.app, "document_types", key))
			}
			if key == "" {
				key = "(no type)"
			}
		case "correspondent":
			if key != "" {
				key = strings.TrimSpace(relatedName(r.app, "correspondents", key))
			}
			if key == "" {
				key = "(no correspondent)"
			}
		case "tag":
			if key != "" {
				key = strings.TrimSpace(relatedName(r.app, "tags", key))
			}
			if key == "" {
				key = "(untagged)"
			}
		default:
			if key == "" {
				key = "(undated)"
			}
		}
		groups = append(groups, ai.CountGroup{Key: key, Count: row.Count})
	}
	// Same name from two ids (a renamed tag, say) folds together.
	merged := map[string]int{}
	for _, g := range groups {
		merged[g.Key] += g.Count
	}
	groups = groups[:0]
	for k, n := range merged {
		groups = append(groups, ai.CountGroup{Key: k, Count: n})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Key < groups[j].Key
	})
	other := 0
	if len(groups) > maxCountGroups {
		for _, g := range groups[maxCountGroups:] {
			other += g.Count
		}
		groups = groups[:maxCountGroups]
	}
	return groups, other
}
