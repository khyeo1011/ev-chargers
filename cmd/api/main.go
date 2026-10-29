// Command api serves the chargers REST API.
//
// With TABLE_NAME unset it runs fully in memory (no AWS needed), which is the
// quickest way to try it: `go run ./cmd/api`.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"

	"github.com/khyeo1011/ev-chargers/internal/api"
	"github.com/khyeo1011/ev-chargers/internal/cloud"
	"github.com/khyeo1011/ev-chargers/internal/memory"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// Kubernetes sends SIGTERM before killing a pod; catching it lets
	// in-flight requests finish instead of being cut off.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &api.Server{}
	if table := os.Getenv("TABLE_NAME"); table == "" {
		slog.Warn("TABLE_NAME not set: using in-memory store, nothing goes to AWS")
		srv.Store = memory.NewStore()
		srv.Publisher = &memory.Publisher{}
	} else {
		// Reads credentials and region from the environment, ~/.aws, or an
		// IAM role, in that order. Same code works locally, in k3s and on EKS.
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			slog.Error("load AWS config", "err", err)
			os.Exit(1)
		}
		srv.Store = &cloud.DynamoStore{Client: dynamodb.NewFromConfig(cfg), Table: table}
		srv.Publisher = &cloud.EventBridgePublisher{
			Client:  eventbridge.NewFromConfig(cfg),
			BusName: getenv("EVENT_BUS_NAME", "default"),
		}
		slog.Info("using AWS", "table", table, "region", cfg.Region)
	}

	httpSrv := &http.Server{
		Addr:              ":" + getenv("PORT", "8080"),
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
