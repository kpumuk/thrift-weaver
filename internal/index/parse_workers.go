package index

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/kpumuk/thrift-weaver/internal/syntax"
)

func (m *Manager) summarizeScannedFiles(ctx context.Context, files []scannedFile, cached map[DocumentKey]loadedDiskState) (map[DocumentKey]loadedDiskState, error) {
	next := make(map[DocumentKey]loadedDiskState, len(files))
	pending := make([]scannedFile, 0, len(files))
	for _, file := range files {
		if state, ok := cached[file.Key]; ok && sameScannedFileMetadata(state.file, file) {
			next[file.Key] = state
			continue
		}
		pending = append(pending, file)
	}

	states, err := m.parseScannedFiles(ctx, pending)
	if err != nil {
		return nil, err
	}
	for i, state := range states {
		next[pending[i].Key] = state
	}
	return next, nil
}

func (m *Manager) parseScannedFiles(ctx context.Context, files []scannedFile) ([]loadedDiskState, error) {
	if len(files) == 0 {
		return nil, nil
	}
	workers := 1
	if m != nil {
		workers = min(m.parseWorkers, len(files))
	}
	if workers <= 1 {
		return parseScannedFilesSequential(ctx, files)
	}
	return parseScannedFilesParallel(ctx, files, workers)
}

func parseScannedFilesSequential(ctx context.Context, files []scannedFile) ([]loadedDiskState, error) {
	parser := syntax.NewReusableParser()
	defer parser.Close()

	out := make([]loadedDiskState, len(files))
	for i, file := range files {
		state, err := summarizeScannedFile(ctx, parser, file)
		if err != nil {
			return nil, err
		}
		out[i] = state
	}
	return out, nil
}

func parseScannedFilesParallel(ctx context.Context, files []scannedFile, workers int) ([]loadedDiskState, error) {
	ctx = contextOrBackground(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]loadedDiskState, len(files))
	errs := make([]error, len(files))
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			parser := syntax.NewReusableParser()
			defer parser.Close()

			for idx := range jobs {
				state, err := summarizeScannedFile(ctx, parser, files[idx])
				if err != nil {
					errs[idx] = err
					cancel()
					continue
				}
				results[idx] = state
			}
		})
	}

	for i := range files {
		if err := ctx.Err(); err != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func summarizeScannedFile(ctx context.Context, parser *syntax.ReusableParser, file scannedFile) (loadedDiskState, error) {
	if err := ctx.Err(); err != nil {
		return loadedDiskState{}, err
	}

	src, err := os.ReadFile(file.Path)
	if err != nil {
		return loadedDiskState{}, fmt.Errorf("read %s: %w", file.Path, err)
	}

	summary, err := ParseAndSummarizeWithParser(ctx, parser, file.Key, DocumentInput{
		URI:        file.DisplayURI,
		Version:    -1,
		Generation: 0,
		Source:     src,
	})
	if err != nil {
		return loadedDiskState{}, err
	}
	return loadedDiskState{file: file, summary: summary}, nil
}
