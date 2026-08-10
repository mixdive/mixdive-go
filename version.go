package mixdive

import "runtime/debug"

// detectAppVersion resolves the app version the client reports in the
// X-App-Version header (Mixdive ingest contract, P2): the host binary's VCS
// revision from Go build info — the short commit hash, with "+dirty"
// appended when the working tree was modified at build time.
//
// Build info carries VCS data only when the binary was built from a git
// checkout with `go build` (the default since Go 1.18); `go run`, test
// binaries and builds without a repository yield nothing, in which case no
// header is sent and occurrences simply carry no version.
func detectAppVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "+dirty"
	}
	return revision
}
