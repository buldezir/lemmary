//go:build !vectors

package fulltext

// The backend is built with `-tags vectors`, always. bleve compiles its kNN API
// out of the package unless that tag is set (search_no_knn.go upstream), so a
// tag-less build would not fail where the vector code lives -- it would fail
// with a scatter of "undefined: SearchRequest.KNN"-style errors, or, worse,
// silently build a binary whose search is missing half its recall.
//
// This file exists so that build stops here instead, with one message that says
// what to do. The identifier below is deliberately undefined: it never resolves,
// under any tag combination, because the whole file is excluded once `vectors`
// is set.
//
// The tag needs cgo and blevesearch's FAISS fork on the machine. See
// scripts/faiss-build.sh, the repo .envrc, and the developer setup section in
// docs/setup.md.
const _ = build_this_module_with_tags_vectors__see_docs_setup_md
