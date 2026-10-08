package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/appatalks/backupfabric/internal/api"
	"github.com/appatalks/backupfabric/internal/catalog"
	"github.com/appatalks/backupfabric/internal/live"
	"github.com/appatalks/backupfabric/internal/model"
	"github.com/appatalks/backupfabric/internal/orchestrator"
	"github.com/appatalks/backupfabric/internal/provider"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "status":
		return status(args[1:])
	case "appliances":
		return appliances(args[1:])
	case "backups":
		return backups(args[1:])
	case "storage":
		return storage(args[1:])
	case "diagnostics":
		return diagnostics(args[1:])
	case "live":
		return liveCommands(args[1:])
	case "version":
		fmt.Println(api.Version)
		return nil
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New(`usage: backupfabric <command>

commands:
  serve
  status
  appliances list
  appliances add
  backups list
  backups collect
  storage inspect
  diagnostics
  live endpoints|settings|jobs|preflight|run
  version`)
}

func liveCommands(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	flags := flag.NewFlagSet("live "+args[0], flag.ContinueOnError)
	client := addClientFlags(flags)
	endpoint := flags.String("endpoint", "", "registered endpoint ID for preflight")
	action := flags.String("action", "", "backup, collect, or stage")
	source := flags.String("source", "", "registered source ID")
	target := flags.String("target", "", "registered restore target ID")
	collection := flags.String("collection", "", "completed collection job ID")
	timestamp := flags.String("snapshot", "", "native timestamp for staging")
	approval := flags.String("confirm", "", "exact typed approval phrase")
	quiesced := flags.Bool("quiesced", false, "attest that required writers are paused and sync is complete")
	compatible := flags.Bool("compatible-target", false, "attest that GHES restore versions are compatible")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	method, path := http.MethodGet, "/api/v1/live/"+args[0]
	var input any
	switch args[0] {
	case "endpoints", "settings", "jobs":
	case "preflight":
		if *endpoint == "" {
			return fmt.Errorf("--endpoint is required")
		}
		method, path, input = http.MethodPost, "/api/v1/live/endpoints/"+*endpoint+"/preflight", struct{}{}
	case "run":
		method, path = http.MethodPost, "/api/v1/live/jobs"
		input = live.Request{
			Action: *action, SourceID: *source, TargetID: *target,
			CollectionID: *collection, Timestamp: *timestamp, Confirmation: *approval,
			Quiesced: *quiesced, CompatibleTarget: *compatible,
		}
	default:
		return usageError()
	}
	var result json.RawMessage
	if err := request(method, strings.TrimRight(client.apiURL, "/")+path, input, &result); err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, result, "", "  "); err != nil {
		return err
	}
	fmt.Println(formatted.String())
	return nil
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listenAddress := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	databasePath := flags.String("database", "./var/backupfabric.db", "SQLite catalog path")
	archiveRoot := flags.String("archive-root", "/var/lib/backupfabric/archives", "isolated receiver collection root (absolute path)")
	secretsDir := flags.String("ssh-secrets", "/run/secrets/backupfabric", "read-only SSH key and known_hosts directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listenAddress)
	if err != nil {
		return fmt.Errorf("parse listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("foundation API must listen on loopback; got %q", host)
	}
	if err := os.MkdirAll(filepath.Dir(*databasePath), 0o700); err != nil {
		return fmt.Errorf("create catalog directory: %w", err)
	}
	c, err := catalog.Open(*databasePath)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := os.Chmod(*databasePath, 0o600); err != nil {
		return fmt.Errorf("restrict catalog permissions: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	snapshotProvider := provider.NewDevelopmentProvider()
	service := orchestrator.New(c, snapshotProvider)
	store, err := c.LiveStore()
	if err != nil {
		return err
	}
	liveService, err := live.NewService(store, *archiveRoot, *secretsDir, logger, live.ExecRunner{})
	if err != nil {
		return err
	}
	defer liveService.Close()
	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           api.New(c, service, snapshotProvider.Name(), logger, liveService),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "error", err)
		}
	}()
	logger.Info("server starting", "address", server.Addr, "provider", snapshotProvider.Name())
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type clientFlags struct {
	apiURL string
}

func addClientFlags(flags *flag.FlagSet) *clientFlags {
	defaultURL := os.Getenv("BACKUPFABRIC_API_URL")
	if defaultURL == "" {
		defaultURL = "http://127.0.0.1:8080"
	}
	value := &clientFlags{}
	flags.StringVar(&value.apiURL, "api-url", defaultURL, "BackupFabric API URL")
	return value
}

func status(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	client := addClientFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	var health model.Health
	if err := request(http.MethodGet, client.apiURL+"/api/v1/health", nil, &health); err != nil {
		return err
	}
	fmt.Printf("status: %s\nversion: %s\nprovider: %s\n", health.Status, health.Version, health.Provider)
	return nil
}

func appliances(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("appliances list", flag.ContinueOnError)
		client := addClientFlags(flags)
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		var result []model.Appliance
		if err := request(http.MethodGet, client.apiURL+"/api/v1/appliances", nil, &result); err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tNAME\tHOSTNAME\tVOLUME\tSTATE")
		for _, appliance := range result {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
				appliance.ID, appliance.Name, appliance.Hostname, appliance.VolumeID, appliance.State)
		}
		return writer.Flush()
	case "add":
		flags := flag.NewFlagSet("appliances add", flag.ContinueOnError)
		client := addClientFlags(flags)
		name := flags.String("name", "", "unique display name")
		hostname := flags.String("hostname", "", "trusted GHES hostname")
		volume := flags.String("volume", "", "provider volume identifier")
		applianceUUID := flags.String("appliance-uuid", "", "GHES appliance UUID")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		input := map[string]string{
			"name": *name, "hostname": *hostname, "volume_id": *volume,
			"appliance_uuid": *applianceUUID,
		}
		var result model.Appliance
		if err := request(http.MethodPost, client.apiURL+"/api/v1/appliances", input, &result); err != nil {
			return err
		}
		fmt.Printf("registered %s (%s)\n", result.Name, result.ID)
		return nil
	default:
		return usageError()
	}
}

func backups(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("backups list", flag.ContinueOnError)
		client := addClientFlags(flags)
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		var result []model.Backup
		if err := request(http.MethodGet, client.apiURL+"/api/v1/backups", nil, &result); err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tAPPLIANCE\tNATIVE SNAPSHOT\tPROVIDER SNAPSHOT\tSTATE")
		for _, backup := range result {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
				backup.ID, backup.ApplianceID, backup.NativeTimestamp, backup.ProviderID, backup.State)
		}
		return writer.Flush()
	case "collect":
		flags := flag.NewFlagSet("backups collect", flag.ContinueOnError)
		client := addClientFlags(flags)
		applianceID := flags.String("appliance", "", "registered appliance ID")
		timestamp := flags.String("snapshot", "", "native timestamp (YYYYMMDDTHHMMSS)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		var result model.Backup
		path := fmt.Sprintf("%s/api/v1/appliances/%s/collections",
			client.apiURL, *applianceID)
		if err := request(http.MethodPost, path, model.CollectionRequest{
			NativeTimestamp: *timestamp,
		}, &result); err != nil {
			return err
		}
		fmt.Printf("cataloged %s as %s\n", result.ProviderID, result.State)
		return nil
	default:
		return usageError()
	}
}

func storage(args []string) error {
	if len(args) == 0 || args[0] != "inspect" {
		return usageError()
	}
	return backups(append([]string{"list"}, args[1:]...))
}

func diagnostics(args []string) error {
	if err := status(args); err != nil {
		return err
	}
	fmt.Println("warning: development provider records references only")
	fmt.Println("warning: restore qualification requires a real GHES rehearsal")
	return nil
}

func request(method, url string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(url, "/"), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("call API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem); err != nil {
			return fmt.Errorf("API returned %s", response.Status)
		}
		return fmt.Errorf("API returned %s: %s", response.Status, problem.Error)
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
