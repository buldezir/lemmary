//go:build !vectors

package fulltext

// The backend is built with `-tags vectors`, always: bleve compiles its kNN API
// out unless that tag is set, and a tag-less build would otherwise scatter
// "undefined: SearchRequest.KNN" errors, or silently build a binary whose
// search is missing half its recall. The identifier below is deliberately
// undefined so the build stops here with one message instead.
//
// The tag needs cgo and blevesearch's FAISS fork on the machine. See
// scripts/faiss-build.sh, the repo .envrc, and docs/development.md.
const _ = build_this_module_with_tags_vectors__see_docs_setup_md
