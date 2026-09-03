// This is a separate Go module so the cinc-zero test dependency never reaches
// the root cinc-api module's dependency graph. Consumers of
// github.com/cinc-project/cinc-api keep a zero-dependency import; only
// `go test` inside this directory pulls cinc-zero. It is excluded from the
// root module's ./... by virtue of having its own go.mod.
module github.com/cinc-project/cinc-api/integration

go 1.26.3

require (
	github.com/cinc-project/cinc-api v0.0.0
	github.com/tas50/cinc-zero v0.4.0
)

replace github.com/cinc-project/cinc-api => ../
