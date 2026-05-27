package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"

	"github.com/skiff-build/skiff/pkg/config"
	"github.com/skiff-build/skiff/pkg/obs"
	"github.com/skiff-build/skiff/pkg/registry"
	"github.com/skiff-build/skiff/pkg/store"
)

// ---------------------------------------------------------------------------
// Prometheus metrics
// ---------------------------------------------------------------------------

var (
	changesRowsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "skiff_ingest_changes_rows_total",
		Help: "Total number of change rows received from the npm _changes feed.",
	})
	versionsObservedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "skiff_ingest_versions_observed_total",
		Help: "Total number of package versions observed, labeled by result.",
	}, []string{"result"})
	tarballBytesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "skiff_ingest_tarball_bytes_total",
		Help: "Total bytes downloaded from npm tarball CDN.",
	})
)

func init() {
	prometheus.MustRegister(changesRowsTotal, versionsObservedTotal, tarballBytesTotal)
}

// ---------------------------------------------------------------------------
// S3 client builder
// ---------------------------------------------------------------------------

func newS3Client(ctx context.Context, cfg *config.Config) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.S3AccessKeyID,
				cfg.S3SecretAccessKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("ingest: aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = &cfg.S3Endpoint
		o.UsePathStyle = true // Garage requires path-style
	})
	return client, nil
}

// ---------------------------------------------------------------------------
// Metrics server
// ---------------------------------------------------------------------------

func serveMetrics(listenAddr string, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: listenAddr, Handler: mux}
	log.Info("metrics server listening", "addr", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("metrics server error", "err", err)
	}
}

// ---------------------------------------------------------------------------
// handleChange processes a single change row.
// It fetches the packument and processes new (name,version) pairs concurrently
// (up to concurrencyLimit goroutines).
// ---------------------------------------------------------------------------

const concurrencyLimit = 4

func handleChange(
	ctx context.Context,
	row registry.ChangeRow,
	cfg *config.Config,
	st *store.Store,
	s3c *s3.Client,
	httpClient *http.Client,
	log *slog.Logger,
) error {
	// Skip deleted package entries — they have no packument to fetch.
	if row.Deleted {
		log.Debug("skipping deleted package", "name", row.ID, "seq", row.Seq)
		return nil
	}

	log.Debug("processing change", "name", row.ID, "seq", row.Seq)

	packument, err := registry.FetchPackument(ctx, httpClient, cfg.PackumentURL, cfg.RegistryUserAgent, row.ID)
	if err != nil {
		log.Warn("fetch packument failed", "name", row.ID, "err", err)
		// Non-fatal; continue processing other changes.
		return nil
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(concurrencyLimit)

	for ver, pv := range packument.Versions {
		ver := ver
		pv := pv

		// Skip versions without sha512 integrity.
		if pv.Dist.Integrity == "" {
			log.Debug("skipping version: no integrity", "name", row.ID, "version", ver)
			versionsObservedTotal.WithLabelValues("no_integrity").Inc()
			continue
		}

		eg.Go(func() error {
			return processVersion(egCtx, row, packument, ver, pv, cfg, st, s3c, httpClient, log)
		})
	}

	return eg.Wait()
}

func processVersion(
	ctx context.Context,
	row registry.ChangeRow,
	packument *registry.Packument,
	ver string,
	pv registry.PackumentVersion,
	cfg *config.Config,
	st *store.Store,
	s3c *s3.Client,
	httpClient *http.Client,
	log *slog.Logger,
) error {
	// Dedupe: skip if we've already processed this (name, version).
	exists, err := st.ObservationExists(ctx, packument.Name, ver)
	if err != nil {
		log.Warn("observation exists check failed", "name", packument.Name, "version", ver, "err", err)
		// Proceed conservatively.
	} else if exists {
		versionsObservedTotal.WithLabelValues("dedupe").Inc()
		return nil
	}

	// Download tarball and verify integrity.
	tarballData, sha512Hex, err := registry.DownloadTarball(ctx, httpClient, pv.Dist.Tarball, pv.Dist.Integrity)
	if err != nil {
		if errors.Is(err, registry.ErrIntegrityMismatch) {
			log.Warn("integrity mismatch", "name", packument.Name, "version", ver,
				"url", pv.Dist.Tarball)
			versionsObservedTotal.WithLabelValues("integrity_mismatch").Inc()
			return nil
		}
		log.Warn("tarball download failed", "name", packument.Name, "version", ver, "err", err)
		versionsObservedTotal.WithLabelValues("download_error").Inc()
		return nil
	}
	tarballBytesTotal.Add(float64(len(tarballData)))

	// S3 object key: sources/<sha512>/<name>-<version>.tgz
	// Sanitize version for use in S3 key (some versions have special chars).
	s3Key := fmt.Sprintf("sources/%s/%s-%s.tgz", sha512Hex, sanitizeName(packument.Name), sanitizeName(ver))

	// Idempotent PUT: HEAD first, skip if object exists with matching size.
	if err := putObjectIdempotent(ctx, s3c, cfg.S3BucketSources, s3Key, tarballData, log); err != nil {
		log.Warn("s3 put failed", "name", packument.Name, "version", ver, "key", s3Key, "err", err)
		versionsObservedTotal.WithLabelValues("s3_error").Inc()
		return nil
	}

	sourceObjectKey := fmt.Sprintf("s3://%s/%s", cfg.S3BucketSources, s3Key)

	// Record observation in ClickHouse.
	obs := store.Observation{
		ObservedAt:       time.Now().UTC(),
		Name:             packument.Name,
		Version:          ver,
		RegistrySeq:      row.Seq,
		PackumentRev:     packument.Rev,
		TarballURL:       pv.Dist.Tarball,
		TarballSHA512Hex: sha512Hex,
		TarballSizeBytes: uint64(len(tarballData)),
		SourceObjectKey:  sourceObjectKey,
	}
	if err := st.RecordObservation(ctx, obs); err != nil {
		log.Warn("record observation failed", "name", packument.Name, "version", ver, "err", err)
		versionsObservedTotal.WithLabelValues("ch_error").Inc()
		return nil
	}

	// Record event.
	payload := fmt.Sprintf(`{"seq":%d,"sha512":"%s","size":%d}`,
		row.Seq, sha512Hex, len(tarballData))
	if err := st.RecordEvent(ctx, store.Event{
		EventType: "package_observed",
		Name:      packument.Name,
		Version:   ver,
		Payload:   payload,
	}); err != nil {
		log.Warn("record event failed", "name", packument.Name, "version", ver, "err", err)
	}

	log.Info("observed", "name", packument.Name, "version", ver,
		"seq", row.Seq, "sha512", sha512Hex[:16]+"...", "bytes", len(tarballData))
	versionsObservedTotal.WithLabelValues("ok").Inc()
	return nil
}

// putObjectIdempotent uploads data to S3 unless an object already exists at
// the key with the same size.
func putObjectIdempotent(ctx context.Context, s3c *s3.Client, bucket, key string, data []byte, log *slog.Logger) error {
	// HEAD to check existing object.
	headOut, err := s3c.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err == nil {
		// Object exists; check size.
		if headOut.ContentLength != nil && *headOut.ContentLength == int64(len(data)) {
			log.Debug("s3 object exists with matching size, skipping PUT", "key", key)
			return nil
		}
	}
	// Either not found (404) or different size; proceed with PUT.
	contentType := "application/gzip"
	_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 put object %q: %w", key, err)
	}
	return nil
}

// sanitizeName replaces characters that are not safe in S3 keys with underscores.
// Scoped npm packages have a "@" and "/" in the name.
func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// fetchCurrentSeq fetches the latest sequence number from the registry
// by requesting a single change row in descending order.
// This is used as the starting point when no checkpoint has been saved.
func fetchCurrentSeq(ctx context.Context, client *http.Client, baseURL, userAgent string, log *slog.Logger) (uint64, error) {
	// Use descending=true to get the most recent change.
	url := fmt.Sprintf("%s/_changes?descending=true&limit=1", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build head seq request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("head seq request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("head seq status %d", resp.StatusCode)
	}

	var result struct {
		LastSeq interface{} `json:"last_seq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode head seq: %w", err)
	}

	switch v := result.LastSeq.(type) {
	case float64:
		seq := uint64(v)
		log.Info("fetched current registry head seq", "seq", seq)
		return seq, nil
	default:
		return 0, fmt.Errorf("unexpected last_seq type %T", result.LastSeq)
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	log := obs.NewLogger(cfg.LogLevel)
	log.Info("skiff ingest starting", "config", cfg.LogValue())

	// Open ClickHouse store.
	st, err := store.Open(ctx, cfg.ClickHouseDSN)
	if err != nil {
		return fmt.Errorf("store open failed: %w", err)
	}
	defer st.Close()

	// Run schema migrations.
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	log.Info("migrations applied")

	// Build S3 client.
	s3c, err := newS3Client(ctx, cfg)
	if err != nil {
		return fmt.Errorf("s3 client init failed: %w", err)
	}

	// Start metrics server in background.
	go serveMetrics(cfg.MetricsAddr, log)

	// Build a shared HTTP client with a generous timeout for long-poll requests.
	httpClient := &http.Client{
		Timeout: 60 * time.Second, // long-poll can hold up to 30s
	}

	// Load last checkpoint.
	since, err := st.GetChangesCheckpoint(ctx)
	if err != nil {
		return fmt.Errorf("get checkpoint failed: %w", err)
	}
	if since == 0 {
		// No prior checkpoint: start from near the current HEAD to avoid replaying
		// the entire registry history (111M+ changes). Fetch the current last_seq.
		headSeq, headErr := fetchCurrentSeq(ctx, httpClient, cfg.RegistryURL, cfg.RegistryUserAgent, log)
		if headErr != nil {
			log.Warn("could not fetch current head seq, starting from 0", "err", headErr)
		} else {
			since = headSeq
			log.Info("no checkpoint found, starting from current registry head", "since", since)
		}
	} else {
		log.Info("resuming from checkpoint", "since", since)
	}

	// Poll loop with exponential back-off on errors.
	backoff := 1 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			log.Info("context cancelled, shutting down")
			return nil
		default:
		}

		// Use a per-poll context so individual polls can be cancelled on shutdown.
		pollCtx, cancelPoll := context.WithCancel(ctx)

		lastSeq, pollErr := registry.Poll(
			pollCtx,
			httpClient,
			cfg.RegistryURL,
			cfg.RegistryUserAgent,
			since,
			500,
			func(row registry.ChangeRow) error {
				changesRowsTotal.Inc()
				if err := handleChange(pollCtx, row, cfg, st, s3c, httpClient, log); err != nil {
					log.Warn("handleChange error", "name", row.ID, "err", err)
					// Non-fatal per row; keep processing.
				}
				return nil
			},
		)
		cancelPoll()

		if pollErr != nil {
			log.Warn("poll error", "err", pollErr, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}

		// Successful poll — reset backoff, persist checkpoint.
		backoff = 1 * time.Second

		if lastSeq > since {
			if err := st.SetChangesCheckpoint(ctx, lastSeq); err != nil {
				log.Warn("set checkpoint failed", "err", err)
			} else {
				log.Debug("checkpoint saved", "seq", lastSeq)
			}
			since = lastSeq
		}

		// Long-poll already handled the wait server-side; no sleep needed on success.
		// On an empty (heartbeat-only) response, add a minimum interval to avoid
		// hammering on a quiet feed.
		if lastSeq == since {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(cfg.RegistryPollInterval):
			}
		}
	}
}
