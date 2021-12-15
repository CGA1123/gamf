package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"

	_ "github.com/heroku/x/hmetrics/onload" // Heroku go-language-metrics
)

func main() {
	if err := realMain(); err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}

func realMain() error {
	if err := defaultEnv(); err != nil {
		return fmt.Errorf("failed to set env defaults: %w", err)
	}

	env, err := requireEnv(
		"PORT",
		"GAMF_URL",
		"GAMF_HOST",
		"GAMF_ENV",
		"REDIS_URL",
	)
	if err != nil {
		return fmt.Errorf("error fetching environment: %w", err)
	}

	redisClient, err := setupRedis(env["REDIS_URL"])
	if err != nil {
		return fmt.Errorf("error configuring redis: %w", err)
	}

	closer, err := initObs(context.Background(), "gamf-http", env)
	if err != nil {
		return fmt.Errorf("error: %w", err)
	}
	defer closer()

	store := NewRedisStore(redisClient)

	r := mux.NewRouter()
	r.HandleFunc("/", HomeHandler).Methods(http.MethodGet)
	r.HandleFunc("/start", StartHandler(env["GAMF_URL"], store)).Methods(http.MethodPost)
	r.HandleFunc("/redirect/{initialKey}", RedirectHandler(store)).Methods(http.MethodGet)
	r.HandleFunc("/callback", CallbackHandler(store)).Methods(http.MethodGet)
	r.HandleFunc("/code/{key}", CodeHandler(store)).Methods(http.MethodPost)
	r.HandleFunc("/done", DoneHandler).Methods(http.MethodGet)

	return RunServer(env["PORT"], r)
}

func loggingHandler(n http.Handler) http.Handler {
	return handlers.LoggingHandler(os.Stdout, n)
}

func timeoutHandler(t time.Duration) func(http.Handler) http.Handler {
	return func(n http.Handler) http.Handler {
		return http.TimeoutHandler(n, t, http.StatusText(http.StatusServiceUnavailable))
	}
}

func obs(n http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("type", "http_server"))

		n.ServeHTTP(w, r)
	})
}

func RunServer(port string, r *mux.Router) error {
	r.Use(
		loggingHandler,
		otelmux.Middleware("gamf-http"),
		obs,
		timeoutHandler(5*time.Second),
	)

	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	})

	server := &http.Server{
		Addr:         "0.0.0.0:" + port,
		WriteTimeout: time.Second * 5,
		ReadTimeout:  time.Second * 5,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	errorC := make(chan error, 1)
	shutdownC := make(chan os.Signal, 1)

	go func(errC chan<- error) {
		errC <- server.ListenAndServe()
	}(errorC)

	signal.Notify(shutdownC, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errorC:
		if err != nil && err != http.ErrServerClosed {
			return err
		}

		return nil
	case <-shutdownC:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		return server.Shutdown(ctx)
	}
}

func ServerForTest(h http.Handler) (*http.Client, func()) {
	s := httptest.NewTLSServer(h)

	// #nosec G402 this is a test server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	client := s.Client()
	client.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial(network, s.Listener.Addr().String())
		},
	}

	return s.Client(), s.Close
}

func initObs(ctx context.Context, service string, env map[string]string) (func(), error) {
	exp, err := otelExporter(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("failed to setup otelgrpc exporter: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(exp)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(
			// Default attributes for every span.
			//
			// The following are provided by runtime-dyno-metadata feature in Heroku
			// See: https://devcenter.heroku.com/articles/dyno-metadata
			// - HEROKU_APP_NAME
			// - HEROKU_DYNO_ID
			// - HEROKU_RELEASE_VERSION
			// - HEROKU_SLUG_COMMIT
			//
			// The Heroku runtime provides the following environment variables:
			// See: https://devcenter.heroku.com/articles/dynos#local-environment-variables
			// - DYNO
			//
			// These environment variables are expected to be managed
			// by the application owner:
			// - GAMF_HEROKU_ACCOUNT
			// - GAMF_HEROKU_STACK
			// - GAMF_ENV
			resource.NewWithAttributes(
				"https://opentelemetry.io/schemas/v1.4.0",

				// Cloud keys
				semconv.CloudProviderKey.String("heroku"),
				semconv.CloudAccountIDKey.String(os.Getenv("GAMF_HEROKU_ACCOUNT")),
				semconv.CloudPlatformKey.String(os.Getenv("GAMF_HEROKU_STACK")),

				// Service keys
				semconv.ServiceNamespaceKey.String(os.Getenv("HEROKU_APP_NAME")),
				semconv.ServiceNameKey.String(service),
				semconv.ServiceInstanceIDKey.String(os.Getenv("HEROKU_DYNO_ID")),
				semconv.ServiceVersionKey.String(os.Getenv("HEROKU_RELEASE_VERSION")),
				attribute.String("service.revision", os.Getenv("HEROKU_SLUG_COMMIT")),

				// Deployment environment
				semconv.DeploymentEnvironmentKey.String(os.Getenv("GAMF_ENV")),
			),
		),
	)

	otel.SetTracerProvider(tp)
	propagator := propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	otel.SetTextMapPropagator(propagator)

	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "error shutting down tracing: %v", err)
		}
	}, nil
}

func otelExporter(ctx context.Context, env map[string]string) (sdktrace.SpanExporter, error) {
	if env["GAMF_ENV"] == "production" {
		// OTEL OTLP exporters can be configured with the following ENV vars:
		// - OTEL_EXPORTER_OTLP_ENDPOINT (e.g. https://api.honeycomb.io:443)
		// - OTEL_EXPORTER_OTLP_HEADERS (e.g. x-honeycomb-team=<API-KEY>,x-honeycomb-dataset=<dataset>)
		// - OTEL_EXPORTER_OTLP_COMPRESSION (e.g. gzip)
		// - OTEL_EXPORTER_OTLP_PROTOCOL (e.g. grpc)
		// - OTEL_EXPORTER_OTLP_CERTIFICATE (e.g. /etc/ssl/certs/ca-certificates.crt)
		return otlptracegrpc.New(ctx)
	}

	return stdouttrace.New()
}

func requireEnv(names ...string) (map[string]string, error) {
	result := map[string]string{}
	missing := []string{}

	for _, name := range names {
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		} else {
			result[name] = v
		}
	}

	if len(missing) > 0 {
		return map[string]string{}, fmt.Errorf("variables not set: %v", missing)
	}

	return result, nil
}

func defaultEnv() error {
	defaults := map[string]string{
		"GAMF_HOST": "localhost:1123",
		"GAMF_URL":  "http://localhost:1123",
		"GAMF_ENV":  "development",
		"PORT":      "1123",
		"REDIS_URL": "redis://localhost:6379",
	}
	for k, v := range defaults {
		if _, ok := os.LookupEnv(k); ok {
			continue
		}

		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}

	return nil
}

func setupRedis(url string) (*redis.Client, error) {
	config, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("error parsing redis url: %w", err)
	}

	return redis.NewClient(config), nil
}
