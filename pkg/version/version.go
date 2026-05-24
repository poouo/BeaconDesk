package version

// These values are intended to be overridden by release builds with -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
