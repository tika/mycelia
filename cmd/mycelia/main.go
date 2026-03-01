package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tika/mycelia/internal/indexer"
	"github.com/tika/mycelia/internal/watcher"
)

func configureLogger() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})
}

func main() {
	configureLogger()

	targetPath := flag.String("path", ".", "Path to watch for changes")
	debounceMs := flag.Int("debounce", 500, "Debounce delay in milliseconds")
	flag.Parse()

	info, err := os.Stat(*targetPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", *targetPath).Msg("cannot access watch path")
	}
	if !info.IsDir() {
		log.Fatal().Str("path", *targetPath).Msg("path is not a directory")
	}

	config := watcher.DefaultConfig()
	config.DebounceDelay = config.DebounceDelay * time.Duration(*debounceMs) / 500

	idx, err := indexer.New(*targetPath, config)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create indexer")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := idx.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to start indexer")
	}

	log.Info().
		Str("path", *targetPath).
		Int("debounce_ms", *debounceMs).
		Msg("mycelia indexer started")

	fmt.Printf("Mycelia indexer watching: %s\n", *targetPath)
	fmt.Printf("Debounce delay: %dms\n", *debounceMs)
	fmt.Println("Press Ctrl+C to stop...")
	fmt.Println()

	for {
		select {
		case result := <-idx.Results():
			if result.Skipped {
				log.Warn().
					Str("event_type", result.EventType).
					Str("file_path", result.FilePath).
					Str("reason_code", "UNSUPPORTED_FILE_TYPE").
					Msg("file skipped")
				continue
			}

			if result.Error != nil {
				log.Error().
					Err(result.Error).
					Str("event_type", result.EventType).
					Str("file_path", result.FilePath).
					Msg("indexing failed")
				continue
			}

			pr := result.ParseResult
			fmt.Printf("[%s] %s (%s)\n", result.EventType, pr.FilePath, pr.Language)
			fmt.Printf("    Hash: %s...\n", pr.Hash[:16])

			if len(pr.Imports) > 0 {
				fmt.Printf("    Imports (%d):\n", len(pr.Imports))
				for _, imp := range pr.Imports {
					if imp.Alias != "" {
						fmt.Printf("      * as %s from %q [%d:%d]\n",
							imp.Alias, imp.Source, imp.StartByte, imp.EndByte)
					} else if len(imp.Names) > 0 {
						fmt.Printf("      %v from %q [%d:%d]\n",
							imp.Names, imp.Source, imp.StartByte, imp.EndByte)
					} else {
						fmt.Printf("      %q [%d:%d]\n",
							imp.Source, imp.StartByte, imp.EndByte)
					}
				}
			}

			if len(pr.Symbols) > 0 {
				fmt.Printf("    Symbols (%d):\n", len(pr.Symbols))
				for _, sym := range pr.Symbols {
					exported := ""
					if sym.IsExported {
						exported = " (exported)"
					}
					fmt.Printf("      %s %s%s [%d:%d] lines %d-%d\n",
						sym.Kind, sym.Name, exported,
						sym.StartByte, sym.EndByte,
						sym.StartLine+1, sym.EndLine+1)

					for _, child := range sym.Children {
						fmt.Printf("        %s %s [%d:%d]\n",
							child.Kind, child.Name,
							child.StartByte, child.EndByte)
					}
				}
			}
			fmt.Println()

		case <-sigChan:
			log.Info().Msg("shutting down")
			if err := idx.Stop(); err != nil {
				log.Error().Err(err).Msg("failed to stop indexer cleanly")
			}
			return
		}
	}
}
