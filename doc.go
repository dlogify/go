// Package logify sends your Go application's logs and panics to dlogify
// (https://dlogify.com), where errors are grouped, classified and turned into
// tickets.
//
// Import it as dlogify.com/sdk/go; the package name is logify:
//
//	import logify "dlogify.com/sdk/go"
//
//	func main() {
//		logify.Init(logify.Options{Service: "checkout"})
//		defer logify.Shutdown(2 * time.Second)
//		defer logify.Recover()
//		slog.SetDefault(slog.New(logify.NewSlogHandler(slog.NewJSONHandler(os.Stdout, nil))))
//	}
//
// The configuration comes from Options and the LOGIFY_* environment variables
// (LOGIFY_API_KEY is required). Records are sent in the background over
// OTLP/HTTP; no function of this package blocks the caller on the network,
// panics or returns an error.
//
// Init installs no capture by itself: install the slog handler, call
// CaptureStdLog for the standard log package, use one of the adapters
// (dlogify.com/sdk/go/zap, …/zerolog, …/logrus), and defer Recover in main and
// in goroutines (or start them with Go). Go has no exit hook: defer Shutdown
// in main so queued records are sent before the process ends.
package logify
