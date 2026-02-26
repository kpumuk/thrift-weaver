// Package main runs reproducible parse/format and LSP memory stability measurements for Thrift Weaver.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kpumuk/thrift-weaver/internal/format"
	"github.com/kpumuk/thrift-weaver/internal/lsp"
	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

const (
	setSmall     = "small"
	setTypical   = "typical"
	setLarge     = "large"
	setMalformed = "malformed"

	smallThreshold   = 4 * 1024
	largeThreshold   = 32 * 1024
	maxExternalSmall = 32
	maxExternalType  = 20
	maxExternalLarge = 10
)

type config struct {
	externalThriftRoot string
	iterations         int
	warmup             int
	lineWidth          int
	jsonPath           string
	memIters           int
	memSampleEvery     int
	memFreeOSMemory    bool
}

type corpusFile struct {
	Path      string `json:"path"`
	Set       string `json:"set"`
	Source    string `json:"source"`
	Bytes     int    `json:"bytes"`
	Malformed bool   `json:"malformed"`
}

type sampleStats struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	MinMS   float64 `json:"min_ms"`
	MaxMS   float64 `json:"max_ms"`
	MeanMS  float64 `json:"mean_ms"`
}

type benchSetReport struct {
	Set          string      `json:"set"`
	Files        int         `json:"files"`
	Iterations   int         `json:"iterations"`
	Samples      int         `json:"samples"`
	SkippedFiles int         `json:"skipped_files,omitempty"`
	Stats        sampleStats `json:"stats"`
	Notes        []string    `json:"notes,omitempty"`
}

type memSample struct {
	Iteration int    `json:"iteration"`
	HeapAlloc uint64 `json:"heap_alloc"`
	HeapInuse uint64 `json:"heap_inuse"`
	HeapSys   uint64 `json:"heap_sys"`
	NumGC     uint32 `json:"num_gc"`
	LiveDocs  int    `json:"live_docs"`
}

type memoryReport struct {
	Iterations          int         `json:"iterations"`
	SampleEvery         int         `json:"sample_every"`
	DocCount            int         `json:"doc_count"`
	Samples             []memSample `json:"samples"`
	HeapAllocGrowth     int64       `json:"heap_alloc_growth"`
	HeapInuseGrowth     int64       `json:"heap_inuse_growth"`
	UnboundedGrowthHint bool        `json:"unbounded_growth_hint"`
}

type report struct {
	GeneratedAt  time.Time               `json:"generated_at"`
	GoVersion    string                  `json:"go_version"`
	GOOS         string                  `json:"goos"`
	GOARCH       string                  `json:"goarch"`
	CPUs         int                     `json:"cpus"`
	Config       map[string]any          `json:"config"`
	Corpus       map[string][]corpusFile `json:"corpus"`
	CorpusCounts map[string]int          `json:"corpus_counts"`
	ParseBench   []benchSetReport        `json:"parse_bench"`
	FormatBench  []benchSetReport        `json:"format_bench"`
	Memory       memoryReport            `json:"memory"`
	Warnings     []string                `json:"warnings,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "perf-report: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.externalThriftRoot, "external-thrift-root", "", "optional path to a Thrift repo/corpus (uses test/audit for malformed set)")
	flag.IntVar(&cfg.iterations, "iterations", 15, "benchmark iterations per file")
	flag.IntVar(&cfg.warmup, "warmup", 2, "warmup iterations per file")
	flag.IntVar(&cfg.lineWidth, "line-width", 100, "formatter line width")
	flag.StringVar(&cfg.jsonPath, "json", "", "optional JSON report output path")
	flag.IntVar(&cfg.memIters, "memory-iterations", 300, "LSP open/change/close loop iterations")
	flag.IntVar(&cfg.memSampleEvery, "memory-sample-every", 25, "memory sample cadence")
	flag.BoolVar(&cfg.memFreeOSMemory, "memory-free-os", false, "call debug.FreeOSMemory before memory samples (slower, less noisy)")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	if cfg.iterations <= 0 {
		return errors.New("iterations must be > 0")
	}
	if cfg.warmup < 0 {
		return errors.New("warmup must be >= 0")
	}
	if cfg.memIters <= 0 {
		return errors.New("memory-iterations must be > 0")
	}
	if cfg.memSampleEvery <= 0 {
		return errors.New("memory-sample-every must be > 0")
	}

	ctx := context.Background()
	corpus, warnings, err := buildCorpus(cfg.externalThriftRoot)
	if err != nil {
		return err
	}

	parseBench, err := runParseBench(ctx, corpus, cfg)
	if err != nil {
		return err
	}
	formatBench, err := runFormatBench(ctx, corpus, cfg)
	if err != nil {
		return err
	}
	memBench, memWarnings, err := runLSPMemoryLoop(ctx, corpus, cfg)
	if err != nil {
		return err
	}
	warnings = append(warnings, memWarnings...)

	rep := report{
		GeneratedAt:  time.Now().UTC(),
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		CPUs:         runtime.NumCPU(),
		Config:       configJSON(cfg),
		Corpus:       corpus,
		CorpusCounts: mapCorpusCounts(corpus),
		ParseBench:   parseBench,
		FormatBench:  formatBench,
		Memory:       memBench,
		Warnings:     warnings,
	}

	printReport(rep)
	if cfg.jsonPath != "" {
		if err := writeJSON(cfg.jsonPath, rep); err != nil {
			return err
		}
		fmt.Printf("\nJSON report written to %s\n", cfg.jsonPath)
	}

	return nil
}

func buildCorpus(externalRoot string) (map[string][]corpusFile, []string, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, err
	}

	corpus := map[string][]corpusFile{
		setSmall:     {},
		setTypical:   {},
		setLarge:     {},
		setMalformed: {},
	}
	var warnings []string

	added := make(map[string]struct{})
	addFile := func(set, source, path string, malformed bool) error {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if _, ok := added[set+"|"+abs]; ok {
			return nil
		}
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		corpus[set] = append(corpus[set], corpusFile{
			Path:      abs,
			Set:       set,
			Source:    source,
			Bytes:     int(info.Size()),
			Malformed: malformed,
		})
		added[set+"|"+abs] = struct{}{}
		return nil
	}

	// Repo fixtures guarantee all 4 required buckets exist, even without external corpus.
	repoInputs := filepath.Join(repoRoot, "testdata", "format", "input")
	repoFixtures := map[string]string{
		"001_top_level_grouping.thrift":            setSmall,
		"002_comment_preservation.thrift":          setSmall,
		"003_legacy_spellings_and_literals.thrift": setSmall,
		"004_apache_thrifttest.thrift":             setTypical,
		"005_apache_fb303.thrift":                  setTypical,
		"006_apache_cassandra.thrift":              setLarge,
	}
	for name, set := range repoFixtures {
		if err := addFile(set, "repo-fixture", filepath.Join(repoInputs, name), false); err != nil {
			return nil, nil, fmt.Errorf("repo fixture %s: %w", name, err)
		}
	}

	if strings.TrimSpace(externalRoot) == "" {
		warnings = append(warnings, "external Thrift corpus not provided; malformed set and corpus breadth are limited to repo fixtures")
		for _, f := range corpus[setSmall] {
			if strings.Contains(filepath.Base(f.Path), "legacy_spellings") {
				corpus[setMalformed] = append(corpus[setMalformed], corpusFile{
					Path:      f.Path,
					Set:       setMalformed,
					Source:    "repo-fixture-fallback",
					Bytes:     f.Bytes,
					Malformed: true,
				})
				break
			}
		}
		sortCorpus(corpus)
		return corpus, warnings, nil
	}

	absExternal, err := filepath.Abs(externalRoot)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(absExternal)
	if err != nil {
		return nil, nil, fmt.Errorf("external-thrift-root: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("external-thrift-root is not a directory: %s", absExternal)
	}

	var normalFiles []corpusFile
	var malformedFiles []corpusFile
	err = filepath.WalkDir(absExternal, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".thrift" {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absExternal, path)
		if err != nil {
			rel = path
		}
		relSlash := filepath.ToSlash(rel)
		cf := corpusFile{
			Path:   path,
			Source: "external-thrift",
			Bytes:  int(st.Size()),
		}
		if strings.HasPrefix(relSlash, "test/audit/") {
			base := filepath.Base(path)
			if strings.HasPrefix(base, "break") || base == "warning.thrift" || base == "test.thrift" {
				cf.Set = setMalformed
				cf.Malformed = true
				malformedFiles = append(malformedFiles, cf)
				return nil
			}
		}
		normalFiles = append(normalFiles, cf)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk external-thrift-root: %w", err)
	}

	sort.Slice(normalFiles, func(i, j int) bool {
		if normalFiles[i].Bytes != normalFiles[j].Bytes {
			return normalFiles[i].Bytes < normalFiles[j].Bytes
		}
		return normalFiles[i].Path < normalFiles[j].Path
	})
	sort.Slice(malformedFiles, func(i, j int) bool { return malformedFiles[i].Path < malformedFiles[j].Path })

	smallCount, typicalCount, largeCount := 0, 0, 0
	for _, f := range normalFiles {
		switch {
		case f.Bytes < smallThreshold && smallCount < maxExternalSmall:
			if err := addFile(setSmall, f.Source, f.Path, false); err != nil {
				return nil, nil, err
			}
			smallCount++
		case f.Bytes >= smallThreshold && f.Bytes < largeThreshold && typicalCount < maxExternalType:
			if err := addFile(setTypical, f.Source, f.Path, false); err != nil {
				return nil, nil, err
			}
			typicalCount++
		case f.Bytes >= largeThreshold && largeCount < maxExternalLarge:
			if err := addFile(setLarge, f.Source, f.Path, false); err != nil {
				return nil, nil, err
			}
			largeCount++
		}
	}

	for _, f := range malformedFiles {
		if err := addFile(setMalformed, f.Source, f.Path, true); err != nil {
			return nil, nil, err
		}
	}
	if len(malformedFiles) == 0 {
		warnings = append(warnings, "no malformed files discovered under external root test/audit; malformed set uses repo fallback only")
	}

	sortCorpus(corpus)
	return corpus, warnings, nil
}

func sortCorpus(corpus map[string][]corpusFile) {
	for k := range corpus {
		sort.Slice(corpus[k], func(i, j int) bool { return corpus[k][i].Path < corpus[k][j].Path })
	}
}

func mapCorpusCounts(corpus map[string][]corpusFile) map[string]int {
	out := make(map[string]int, len(corpus))
	for k, files := range corpus {
		out[k] = len(files)
	}
	return out
}

func runParseBench(ctx context.Context, corpus map[string][]corpusFile, cfg config) ([]benchSetReport, error) {
	sets := []string{setSmall, setTypical, setLarge, setMalformed}
	out := make([]benchSetReport, 0, len(sets))
	for _, set := range sets {
		files := corpus[set]
		samples, notes, err := benchmarkParse(ctx, files, cfg)
		if err != nil {
			return nil, fmt.Errorf("parse bench %s: %w", set, err)
		}
		out = append(out, benchSetReport{
			Set:        set,
			Files:      len(files),
			Iterations: cfg.iterations,
			Samples:    len(samples),
			Stats:      durationStats(samples),
			Notes:      notes,
		})
	}
	return out, nil
}

func benchmarkParse(ctx context.Context, files []corpusFile, cfg config) ([]time.Duration, []string, error) {
	var samples []time.Duration
	var notes []string
	for _, f := range files {
		src, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		for range cfg.warmup {
			if _, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: f.Path}); err != nil {
				return nil, nil, fmt.Errorf("warmup parse %s: %w", f.Path, err)
			}
		}
		for range cfg.iterations {
			start := time.Now()
			if _, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: f.Path}); err != nil {
				return nil, nil, fmt.Errorf("parse %s: %w", f.Path, err)
			}
			samples = append(samples, time.Since(start))
		}
		if f.Malformed {
			notes = append(notes, filepath.Base(f.Path))
		}
	}
	if len(notes) > 3 {
		notes = []string{
			"malformed examples include " + strings.Join(notes[:3], ", "),
			fmt.Sprintf("... and %d more", len(notes)-3),
		}
	}
	return samples, notes, nil
}

func runFormatBench(ctx context.Context, corpus map[string][]corpusFile, cfg config) ([]benchSetReport, error) {
	sets := []string{setSmall, setTypical, setLarge}
	out := make([]benchSetReport, 0, len(sets))
	for _, set := range sets {
		files := corpus[set]
		samples, skipped, notes, err := benchmarkFormat(ctx, files, cfg)
		if err != nil {
			return nil, fmt.Errorf("format bench %s: %w", set, err)
		}
		out = append(out, benchSetReport{
			Set:          set,
			Files:        len(files),
			Iterations:   cfg.iterations,
			Samples:      len(samples),
			SkippedFiles: skipped,
			Stats:        durationStats(samples),
			Notes:        notes,
		})
	}
	return out, nil
}

type parsedFixture struct {
	file corpusFile
	tree *syntax.Tree
}

func benchmarkFormat(ctx context.Context, files []corpusFile, cfg config) ([]time.Duration, int, []string, error) {
	fixtures := make([]parsedFixture, 0, len(files))
	var notes []string
	skipped := 0
	for _, f := range files {
		src, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		tree, err := syntax.Parse(ctx, src, syntax.ParseOptions{URI: f.Path})
		if err != nil {
			return nil, 0, nil, fmt.Errorf("parse %s: %w", f.Path, err)
		}
		_, err = format.Document(ctx, tree, format.Options{LineWidth: cfg.lineWidth})
		if err != nil {
			if format.IsErrUnsafeToFormat(err) {
				skipped++
				notes = append(notes, "skipped unsafe format: "+filepath.Base(f.Path))
				continue
			}
			return nil, 0, nil, fmt.Errorf("format precheck %s: %w", f.Path, err)
		}
		fixtures = append(fixtures, parsedFixture{file: f, tree: tree})
	}

	var samples []time.Duration
	for _, pf := range fixtures {
		for range cfg.warmup {
			if _, err := format.Document(ctx, pf.tree, format.Options{LineWidth: cfg.lineWidth}); err != nil {
				return nil, 0, nil, fmt.Errorf("warmup format %s: %w", pf.file.Path, err)
			}
		}
		for range cfg.iterations {
			start := time.Now()
			if _, err := format.Document(ctx, pf.tree, format.Options{LineWidth: cfg.lineWidth}); err != nil {
				return nil, 0, nil, fmt.Errorf("format %s: %w", pf.file.Path, err)
			}
			samples = append(samples, time.Since(start))
		}
	}
	if len(notes) > 5 {
		notes = notes[:5]
		notes = append(notes, "additional files skipped")
	}
	return samples, skipped, notes, nil
}

func runLSPMemoryLoop(ctx context.Context, corpus map[string][]corpusFile, cfg config) (memoryReport, []string, error) {
	docs, warnings, err := selectMemoryDocs(corpus)
	if err != nil {
		return memoryReport{}, nil, err
	}
	if len(docs) == 0 {
		return memoryReport{}, warnings, errors.New("no memory benchmark documents available")
	}

	type memDoc struct {
		uri    string
		open   []byte
		change []byte
	}
	memDocs := make([]memDoc, 0, len(docs))
	for i, cf := range docs {
		src, err := os.ReadFile(cf.Path)
		if err != nil {
			return memoryReport{}, nil, fmt.Errorf("read memory doc %s: %w", cf.Path, err)
		}
		memDocs = append(memDocs, memDoc{
			uri:    fmt.Sprintf("file:///perf/memory/%d/%s", i, filepath.Base(cf.Path)),
			open:   src,
			change: mutateForMemoryLoop(src),
		})
	}

	store := lsp.NewSnapshotStore()
	samples := make([]memSample, 0, maxInt(1, cfg.memIters/cfg.memSampleEvery))
	recordSample := func(iter int) {
		if cfg.memFreeOSMemory {
			debug.FreeOSMemory()
		} else {
			runtime.GC()
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		samples = append(samples, memSample{
			Iteration: iter,
			HeapAlloc: ms.HeapAlloc,
			HeapInuse: ms.HeapInuse,
			HeapSys:   ms.HeapSys,
			NumGC:     ms.NumGC,
			LiveDocs:  0,
		})
	}

	recordSample(0)
	for iter := 1; iter <= cfg.memIters; iter++ {
		for _, d := range memDocs {
			_, err := store.Open(ctx, d.uri, 1, d.open)
			if err != nil {
				return memoryReport{}, warnings, fmt.Errorf("memory loop open: %w", err)
			}
			_, err = store.Change(ctx, d.uri, 2, []lsp.TextDocumentContentChangeEvent{{Text: string(d.change)}})
			if err != nil {
				return memoryReport{}, warnings, fmt.Errorf("memory loop change: %w", err)
			}
			_, err = store.Change(ctx, d.uri, 3, []lsp.TextDocumentContentChangeEvent{{Text: string(d.open)}})
			if err != nil {
				return memoryReport{}, warnings, fmt.Errorf("memory loop revert: %w", err)
			}
			store.Close(d.uri)
		}
		if iter%cfg.memSampleEvery == 0 || iter == cfg.memIters {
			recordSample(iter)
		}
	}

	rep := memoryReport{
		Iterations:  cfg.memIters,
		SampleEvery: cfg.memSampleEvery,
		DocCount:    len(memDocs),
		Samples:     samples,
	}
	if len(samples) >= 2 {
		first := samples[0]
		last := samples[len(samples)-1]
		rep.HeapAllocGrowth = int64Diff(last.HeapAlloc, first.HeapAlloc)
		rep.HeapInuseGrowth = int64Diff(last.HeapInuse, first.HeapInuse)
		rep.UnboundedGrowthHint = isUnboundedGrowthHint(samples)
	}
	return rep, warnings, nil
}

func selectMemoryDocs(corpus map[string][]corpusFile) ([]corpusFile, []string, error) {
	var selected []corpusFile
	var warnings []string

	addFirst := func(set string, n int) {
		for _, f := range corpus[set] {
			if f.Malformed {
				continue
			}
			selected = append(selected, f)
			if len(selected) >= n {
				return
			}
		}
	}

	// Prefer a mix of large + typical + one malformed doc.
	if files := corpus[setLarge]; len(files) > 0 {
		selected = append(selected, files[0])
	}
	if files := corpus[setTypical]; len(files) > 0 {
		selected = append(selected, files[0])
		if len(files) > 1 {
			selected = append(selected, files[1])
		}
	}
	if len(selected) < 3 {
		addFirst(setSmall, 3)
	}
	if len(selected) == 0 {
		return nil, warnings, errors.New("no suitable docs for memory loop")
	}
	if malformed := corpus[setMalformed]; len(malformed) > 0 {
		selected = append(selected, malformed[0])
	} else {
		warnings = append(warnings, "memory loop malformed sample not available")
	}

	// Deduplicate by path.
	seen := map[string]struct{}{}
	out := make([]corpusFile, 0, len(selected))
	for _, f := range selected {
		if _, ok := seen[f.Path]; ok {
			continue
		}
		seen[f.Path] = struct{}{}
		out = append(out, f)
	}
	return out, warnings, nil
}

func mutateForMemoryLoop(src []byte) []byte {
	// Full-document replacement change; avoids brittle UTF-16 offset assumptions while still exercising parser/store churn.
	const marker = "\n# perf-memory-toggle\n"
	s := string(src)
	if strings.Contains(s, marker) {
		return []byte(strings.ReplaceAll(s, marker, "\n"))
	}
	trimmed := strings.TrimRight(s, "\n")
	return []byte(trimmed + marker)
}

func isUnboundedGrowthHint(samples []memSample) bool {
	if len(samples) < 4 {
		return false
	}
	base := samples[0]
	last := samples[len(samples)-1]
	growthAlloc := int64Diff(last.HeapAlloc, base.HeapAlloc)
	growthInuse := int64Diff(last.HeapInuse, base.HeapInuse)
	const maxExpectedGrowth = 16 << 20 // 16 MiB heuristic after forced GC samples
	return growthAlloc > maxExpectedGrowth || growthInuse > maxExpectedGrowth
}

func durationStats(samples []time.Duration) sampleStats {
	if len(samples) == 0 {
		return sampleStats{}
	}
	ns := make([]int64, len(samples))
	var sum int64
	for i, d := range samples {
		ns[i] = d.Nanoseconds()
		sum += ns[i]
	}
	slices.Sort(ns)
	p50 := quantile(ns, 0.50)
	p95 := quantile(ns, 0.95)
	return sampleStats{
		Samples: len(samples),
		P50MS:   nanosToMS(p50),
		P95MS:   nanosToMS(p95),
		MinMS:   nanosToMS(ns[0]),
		MaxMS:   nanosToMS(ns[len(ns)-1]),
		MeanMS:  nanosToMS(sum / int64(len(ns))),
	}
}

func quantile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * q)
	return sorted[idx]
}

func nanosToMS(ns int64) float64 {
	return float64(ns) / float64(time.Millisecond)
}

func printReport(rep report) {
	fmt.Printf("Thrift Weaver Performance Report\n")
	fmt.Printf("Generated: %s\n", rep.GeneratedAt.Format(time.RFC3339))
	fmt.Printf("Go: %s | %s/%s | CPUs=%d\n", rep.GoVersion, rep.GOOS, rep.GOARCH, rep.CPUs)
	if ext, ok := rep.Config["external_thrift_root"].(string); ok && ext != "" {
		fmt.Printf("External corpus: %s\n", ext)
	}
	fmt.Println()
	fmt.Println("Corpus sets")
	for _, set := range []string{setSmall, setTypical, setLarge, setMalformed} {
		files := rep.Corpus[set]
		totalBytes := 0
		for _, f := range files {
			totalBytes += f.Bytes
		}
		fmt.Printf("- %-9s files=%3d total=%7d bytes\n", set, len(files), totalBytes)
	}
	if len(rep.Warnings) > 0 {
		fmt.Println()
		fmt.Println("Warnings")
		for _, w := range rep.Warnings {
			fmt.Printf("- %s\n", w)
		}
	}
	fmt.Println()
	printBenchTable("Parse + diagnostics (warm)", rep.ParseBench)
	fmt.Println()
	printBenchTable("Format document (warm, parse tree prebuilt)", rep.FormatBench)
	fmt.Println()
	printMemoryReport(rep.Memory)
}

func printBenchTable(title string, rows []benchSetReport) {
	fmt.Println(title)
	fmt.Println("set        files samples  p50(ms)  p95(ms)  mean(ms)   min    max  skipped")
	for _, r := range rows {
		fmt.Printf("%-10s %5d %7d %8.2f %8.2f %8.2f %6.2f %6.2f %7d\n",
			r.Set, r.Files, r.Samples, r.Stats.P50MS, r.Stats.P95MS, r.Stats.MeanMS, r.Stats.MinMS, r.Stats.MaxMS, r.SkippedFiles)
	}
}

func printMemoryReport(rep memoryReport) {
	fmt.Println("LSP memory loop (open/change/close)")
	fmt.Printf("iterations=%d sample_every=%d docs=%d\n", rep.Iterations, rep.SampleEvery, rep.DocCount)
	if len(rep.Samples) == 0 {
		fmt.Println("no samples")
		return
	}
	last := rep.Samples[len(rep.Samples)-1]
	fmt.Printf("final heap_alloc=%d heap_inuse=%d heap_sys=%d num_gc=%d\n", last.HeapAlloc, last.HeapInuse, last.HeapSys, last.NumGC)
	fmt.Printf("growth heap_alloc=%d heap_inuse=%d unbounded_growth_hint=%v\n", rep.HeapAllocGrowth, rep.HeapInuseGrowth, rep.UnboundedGrowthHint)
}

func writeJSON(path string, rep report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}

func configJSON(cfg config) map[string]any {
	return map[string]any{
		"external_thrift_root": cfg.externalThriftRoot,
		"iterations":           cfg.iterations,
		"warmup":               cfg.warmup,
		"line_width":           cfg.lineWidth,
		"json":                 cfg.jsonPath,
		"memory_iterations":    cfg.memIters,
		"memory_sample_every":  cfg.memSampleEvery,
		"memory_free_os":       cfg.memFreeOSMemory,
	}
}

func int64Diff(a, b uint64) int64 {
	const maxInt64AsUint64 = (^uint64(0)) >> 1
	if a >= b {
		d := a - b
		if d > maxInt64AsUint64 {
			return int64(maxInt64AsUint64)
		}
		return int64(d)
	}
	d := b - a
	if d > maxInt64AsUint64 {
		return -int64(maxInt64AsUint64)
	}
	return -int64(d)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
