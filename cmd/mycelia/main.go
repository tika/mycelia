package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tika/mycelia/internal/indexer"
	"github.com/tika/mycelia/internal/watcher"
)

func main() {
	targetPath := flag.String("path", ".", "Path to watch for changes")
	debounceMs := flag.Int("debounce", 500, "Debounce delay in milliseconds")
	flag.Parse()

	info, err := os.Stat(*targetPath)
	if err != nil {
		log.Fatalf("Error: cannot access path %q: %v", *targetPath, err)
	}
	if !info.IsDir() {
		log.Fatalf("Error: %q is not a directory", *targetPath)
	}

	config := watcher.DefaultConfig()
	config.DebounceDelay = config.DebounceDelay * time.Duration(*debounceMs) / 500

	idx, err := indexer.New(*targetPath, config)
	if err != nil {
		log.Fatalf("Failed to create indexer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := idx.Start(ctx); err != nil {
		log.Fatalf("Failed to start indexer: %v", err)
	}

	fmt.Printf("Mycelia indexer watching: %s\n", *targetPath)
	fmt.Printf("Debounce delay: %dms\n", *debounceMs)
	fmt.Println("Press Ctrl+C to stop...")
	fmt.Println()

	for {
		select {
		case result := <-idx.Results():
			if result.Skipped {
				fmt.Printf("[%s] %s (skipped: unsupported type)\n", result.EventType, result.FilePath)
				continue
			}

			if result.Error != nil {
				fmt.Printf("[%s] %s: %v\n", result.EventType, result.FilePath, result.Error)
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
			fmt.Println("\nShutting down...")
			if err := idx.Stop(); err != nil {
				log.Printf("Error stopping indexer: %v", err)
			}
			return
		}
	}
}
