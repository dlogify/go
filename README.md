# dlogify.com/sdk/go

Send your Go application's logs and panics to [dlogify](https://dlogify.com), which groups errors, classifies them with AI and delivers full-context tickets.

```bash
go get dlogify.com/sdk/go
```

```go
import logify "dlogify.com/sdk/go"

func main() {
	logify.Init(logify.Options{Service: "checkout"}) // LOGIFY_API_KEY from the environment
	defer logify.Shutdown(2 * time.Second)
	defer logify.Recover()
	slog.SetDefault(slog.New(logify.NewSlogHandler(slog.NewJSONHandler(os.Stdout, nil))))
}
```

- slog: `logify.NewSlogHandler(next)`; the standard `log` package: `logify.CaptureStdLog()`.
- Panics: `defer logify.Recover()`, `logify.Go(fn)`, `logify.Middleware(handler)`.
- zap, zerolog and logrus: `dlogify.com/sdk/go/zap`, `dlogify.com/sdk/go/zerolog`, `dlogify.com/sdk/go/logrus`.
- Any process: `go install dlogify.com/sdk/go/cmd/logify@latest`, then `logify run -- ./server`.

No dependencies outside the standard library; Go 1.22 or later. Guide: https://docs.dlogify.com/send-logs/go. MIT licence.
