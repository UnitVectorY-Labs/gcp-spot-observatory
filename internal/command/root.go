package command

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/crawl"
	"github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/database"
	"github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/gcp"
	webapp "github.com/UnitVectorY-Labs/gcp-spot-observatory/internal/web"
	"github.com/spf13/cobra"
)

const envPrefix = "SPOT_OBSERVATORY_"

func Execute(version string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root := &cobra.Command{
		Use:           "gcp-spot-observatory",
		Short:         "Collect and inspect GCP Spot VM capacity history",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(migrateCommand(), crawlCommand(), webCommand())
	return root.ExecuteContext(ctx)
}

func migrateCommand() *cobra.Command {
	databaseURL := env("DATABASE_URL", "")
	logLevel := env("LOG_LEVEL", "info")
	cmd := &cobra.Command{Use: "migrate", Short: "Run outstanding database migrations", RunE: func(cmd *cobra.Command, args []string) error {
		log, err := logger(logLevel)
		if err != nil {
			return err
		}
		log.Info("migration started")
		db, err := database.Open(cmd.Context(), databaseURL)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := database.Migrate(db); err != nil {
			return err
		}
		log.Info("migration completed")
		return nil
	}}
	cmd.Flags().StringVar(&databaseURL, "database-url", databaseURL, "PostgreSQL connection URL (env: SPOT_OBSERVATORY_DATABASE_URL)")
	cmd.Flags().StringVar(&logLevel, "log-level", logLevel, "debug, info, warn, or error (env: SPOT_OBSERVATORY_LOG_LEVEL)")
	return cmd
}

func crawlCommand() *cobra.Command {
	databaseURL := env("DATABASE_URL", "")
	project := env("GCP_PROJECT", "")
	backfill := envBool("BACKFILL", false)
	requestRate := envFloat("GCP_REQUEST_RATE", 2)
	requestTimeout := envDuration("GCP_REQUEST_TIMEOUT", 30*time.Second)
	regions := envList("REGIONS")
	machineTypes := envList("MACHINE_TYPES")
	logLevel := env("LOG_LEVEL", "info")
	cmd := &cobra.Command{Use: "crawl", Short: "Discover and collect GCP Spot VM capacity history", RunE: func(cmd *cobra.Command, args []string) error {
		log, err := logger(logLevel)
		if err != nil {
			return err
		}
		log.Info("crawl command starting", "backfill", backfill)
		db, err := database.Open(cmd.Context(), databaseURL)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := database.Migrate(db); err != nil {
			return err
		}
		client, err := gcp.NewClient(cmd.Context(), project, requestRate, requestTimeout)
		if err != nil {
			return err
		}
		client.SetLogger(log)
		_, err = crawl.Run(cmd.Context(), db, client, crawl.Config{Backfill: backfill, Regions: regions, MachineTypes: machineTypes}, log)
		return err
	}}
	cmd.Flags().StringVar(&databaseURL, "database-url", databaseURL, "PostgreSQL connection URL (env: SPOT_OBSERVATORY_DATABASE_URL)")
	cmd.Flags().StringVar(&project, "gcp-project", project, "GCP project used only for API context (env: SPOT_OBSERVATORY_GCP_PROJECT)")
	cmd.Flags().BoolVar(&backfill, "backfill", backfill, "retain all history returned by GCP (env: SPOT_OBSERVATORY_BACKFILL)")
	cmd.Flags().Float64Var(&requestRate, "gcp-request-rate", requestRate, "maximum GCP API requests per second (env: SPOT_OBSERVATORY_GCP_REQUEST_RATE)")
	cmd.Flags().DurationVar(&requestTimeout, "gcp-request-timeout", requestTimeout, "timeout for each GCP request (env: SPOT_OBSERVATORY_GCP_REQUEST_TIMEOUT)")
	cmd.Flags().StringSliceVar(&regions, "region", regions, "limit crawl scope to region (repeatable; env: SPOT_OBSERVATORY_REGIONS)")
	cmd.Flags().StringSliceVar(&machineTypes, "machine-type", machineTypes, "limit crawl scope to machine type (repeatable; env: SPOT_OBSERVATORY_MACHINE_TYPES)")
	cmd.Flags().StringVar(&logLevel, "log-level", logLevel, "debug, info, warn, or error (env: SPOT_OBSERVATORY_LOG_LEVEL)")
	return cmd
}

func webCommand() *cobra.Command {
	databaseURL := env("DATABASE_URL", "")
	listenAddress := env("LISTEN_ADDRESS", "0.0.0.0:8080")
	logLevel := env("LOG_LEVEL", "info")
	cmd := &cobra.Command{Use: "web", Short: "Start the Spot history web explorer", RunE: func(cmd *cobra.Command, args []string) error {
		log, err := logger(logLevel)
		if err != nil {
			return err
		}
		db, err := database.Open(cmd.Context(), databaseURL)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := database.VerifySchema(db); err != nil {
			return err
		}
		server, err := webapp.New(db, log)
		if err != nil {
			return err
		}
		return server.ListenAndServe(cmd.Context(), listenAddress)
	}}
	cmd.Flags().StringVar(&databaseURL, "database-url", databaseURL, "PostgreSQL connection URL (env: SPOT_OBSERVATORY_DATABASE_URL)")
	cmd.Flags().StringVar(&listenAddress, "listen-address", listenAddress, "HTTP listen address (env: SPOT_OBSERVATORY_LISTEN_ADDRESS)")
	cmd.Flags().StringVar(&logLevel, "log-level", logLevel, "debug, info, warn, or error (env: SPOT_OBSERVATORY_LOG_LEVEL)")
	return cmd
}

func logger(value string) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(value) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log level %q", value)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})), nil
}
func env(name, fallback string) string {
	if value, ok := os.LookupEnv(envPrefix + name); ok {
		return value
	}
	return fallback
}
func envBool(name string, fallback bool) bool {
	value, ok := os.LookupEnv(envPrefix + name)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envFloat(name string, fallback float64) float64 {
	value, ok := os.LookupEnv(envPrefix + name)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
func envDuration(name string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(envPrefix + name)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
func envList(name string) []string {
	value := strings.TrimSpace(env(name, ""))
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
