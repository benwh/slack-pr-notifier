package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"cloud.google.com/go/firestore"
	"github-slack-notifier/internal/config"
	"github-slack-notifier/internal/log"
	"google.golang.org/api/iterator"
)

const (
	batchSize         = 500
	minArgsRequired   = 2
	filePermReadWrite = 0600
	// Log levels.
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
	// Gin modes.
	ginModeRelease = "release"
)

var (
	ErrOperationCancelled = errors.New("operation cancelled by user")
)

func main() {
	if len(os.Args) < minArgsRequired {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "wipe-firestore":
		handleWipeFirestore()
	case "dump-firestore":
		handleDumpFirestore()
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Toolbox - Utility commands for github-slack-notifier")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  toolbox <command> [flags]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  wipe-firestore     Delete all documents from all Firestore collections")
	fmt.Println("  dump-firestore     Export all documents from all Firestore collections as JSON")
	fmt.Println("  help               Show this help message")
	fmt.Println("")
	fmt.Println("Flags for wipe-firestore:")
	fmt.Println("  --force            Skip confirmation prompt (DANGEROUS!)")
	fmt.Println("")
	fmt.Println("Flags for dump-firestore:")
	fmt.Println("  --output FILE      Write output to file instead of stdout")
	fmt.Println("  --pretty           Pretty-print JSON output")
	fmt.Println("")
}

func handleWipeFirestore() {
	var force bool

	// Parse flags for the wipe-firestore command
	fs := flag.NewFlagSet("wipe-firestore", flag.ExitOnError)
	fs.BoolVar(&force, "force", false, "Skip confirmation prompt (DANGEROUS!)")
	_ = fs.Parse(os.Args[2:])

	cfg := config.Load()
	ctx := context.Background()

	// Setup structured logging
	var logger *slog.Logger
	isDev := cfg.GinMode != ginModeRelease
	var logLevel slog.Level
	switch cfg.LogLevel {
	case logLevelDebug:
		logLevel = slog.LevelDebug
	case logLevelWarn:
		logLevel = slog.LevelWarn
	case logLevelError:
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	if isDev {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	} else {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	}
	slog.SetDefault(logger)

	log.Info(ctx, "Connecting to Firestore", "project_id", cfg.FirestoreProjectID, "database_id", cfg.FirestoreDatabaseID)
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, cfg.FirestoreProjectID, cfg.FirestoreDatabaseID)
	if err != nil {
		log.Error(ctx, "Failed to create Firestore client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := firestoreClient.Close(); err != nil {
			log.Error(context.Background(), "Error closing Firestore client", "error", err)
		}
	}()

	if !force {
		if err := confirmWipeOperation(cfg); err != nil {
			if errors.Is(err, ErrOperationCancelled) {
				log.Info(ctx, "Operation cancelled by user")
				return
			}
			log.Error(ctx, "Failed to get confirmation", "error", err)
			os.Exit(1)
		}
	}

	if err := wipeAllCollections(ctx, firestoreClient); err != nil {
		log.Error(ctx, "Failed to wipe Firestore data", "error", err)
		os.Exit(1)
	}

	log.Info(ctx, "Successfully wiped all Firestore data")
}

func confirmWipeOperation(cfg *config.Config) error {
	fmt.Printf("\n⚠️  WARNING: This will DELETE ALL DATA from Firestore!\n")
	fmt.Printf("   Project: %s\n", cfg.FirestoreProjectID)
	fmt.Printf("   Database: %s\n", cfg.FirestoreDatabaseID)
	fmt.Printf("\nThis operation cannot be undone!\n\n")

	fmt.Print("Are you absolutely sure you want to continue? (type 'DELETE' to confirm): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(response)
	if response != "DELETE" {
		return ErrOperationCancelled
	}

	return nil
}

func wipeAllCollections(ctx context.Context, client *firestore.Client) error {
	collections := []string{
		"users",
		"repos",
		"trackedmessages",
		"oauth_states",
		"channel_configs",
		"github_installations",
		"slack_workspaces",
	}

	for _, collection := range collections {
		log.Info(ctx, "Wiping collection", "collection", collection)
		count, err := wipeCollection(ctx, client, collection)
		if err != nil {
			return fmt.Errorf("failed to wipe collection %s: %w", collection, err)
		}
		log.Info(ctx, "Collection wiped", "collection", collection, "documents_deleted", count)
	}

	return nil
}

func wipeCollection(ctx context.Context, client *firestore.Client, collectionName string) (int, error) {
	collection := client.Collection(collectionName)
	deletedCount := 0

	for {
		iter := collection.Limit(batchSize).Documents(ctx)
		bulkWriter := client.BulkWriter(ctx)
		docCount := 0

		for {
			doc, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				bulkWriter.End()
				return deletedCount, fmt.Errorf("failed to iterate documents: %w", err)
			}

			_, err = bulkWriter.Delete(doc.Ref)
			if err != nil {
				bulkWriter.End()
				return deletedCount, fmt.Errorf("failed to add delete to bulk writer: %w", err)
			}
			docCount++
		}

		if docCount == 0 {
			bulkWriter.End()
			break
		}

		bulkWriter.Flush()
		bulkWriter.End()

		deletedCount += docCount
		log.Debug(ctx, "Batch deleted", "collection", collectionName, "batch_size", docCount, "total_deleted", deletedCount)

		if docCount < batchSize {
			break
		}
	}

	return deletedCount, nil
}

func handleDumpFirestore() {
	var outputFile string
	var prettyPrint bool

	// Parse flags for the dump-firestore command
	fs := flag.NewFlagSet("dump-firestore", flag.ExitOnError)
	fs.StringVar(&outputFile, "output", "", "Write output to file instead of stdout")
	fs.BoolVar(&prettyPrint, "pretty", false, "Pretty-print JSON output")
	_ = fs.Parse(os.Args[2:])

	cfg := config.Load()
	ctx := context.Background()

	// Setup structured logging
	var logger *slog.Logger
	isDev := cfg.GinMode != ginModeRelease
	var logLevel slog.Level
	switch cfg.LogLevel {
	case logLevelDebug:
		logLevel = slog.LevelDebug
	case logLevelWarn:
		logLevel = slog.LevelWarn
	case logLevelError:
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	if isDev {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	} else {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))
	}
	slog.SetDefault(logger)

	log.Info(ctx, "Connecting to Firestore", "project_id", cfg.FirestoreProjectID, "database_id", cfg.FirestoreDatabaseID)
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, cfg.FirestoreProjectID, cfg.FirestoreDatabaseID)
	if err != nil {
		log.Error(ctx, "Failed to create Firestore client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := firestoreClient.Close(); err != nil {
			log.Error(context.Background(), "Error closing Firestore client", "error", err)
		}
	}()

	dump, err := dumpAllCollections(ctx, firestoreClient)
	if err != nil {
		log.Error(ctx, "Failed to dump Firestore data", "error", err)
		os.Exit(1)
	}

	var jsonData []byte
	if prettyPrint {
		jsonData, err = json.MarshalIndent(dump, "", "  ")
	} else {
		jsonData, err = json.Marshal(dump)
	}
	if err != nil {
		log.Error(ctx, "Failed to marshal JSON", "error", err)
		os.Exit(1)
	}

	if outputFile != "" {
		err = os.WriteFile(outputFile, jsonData, filePermReadWrite)
		if err != nil {
			log.Error(ctx, "Failed to write output file", "file", outputFile, "error", err)
			os.Exit(1)
		}
		log.Info(ctx, "Successfully exported Firestore data", "file", outputFile, "size_bytes", len(jsonData))
	} else {
		fmt.Println(string(jsonData))
	}
}

func dumpAllCollections(ctx context.Context, client *firestore.Client) (map[string]interface{}, error) {
	collections := []string{
		"users",
		"repos",
		"trackedmessages",
		"oauth_states",
		"channel_configs",
		"github_installations",
		"slack_workspaces",
	}

	dump := make(map[string]interface{})

	for _, collection := range collections {
		log.Info(ctx, "Dumping collection", "collection", collection)
		data, count, err := dumpCollection(ctx, client, collection)
		if err != nil {
			return nil, fmt.Errorf("failed to dump collection %s: %w", collection, err)
		}
		dump[collection] = data
		log.Info(ctx, "Collection dumped", "collection", collection, "documents", count)
	}

	return dump, nil
}

func dumpCollection(ctx context.Context, client *firestore.Client, collectionName string) ([]map[string]interface{}, int, error) {
	collection := client.Collection(collectionName)
	var documents []map[string]interface{}
	count := 0

	iter := collection.Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, count, fmt.Errorf("failed to iterate documents: %w", err)
		}

		data := doc.Data()
		// Add document ID to the data
		docData := make(map[string]interface{})
		docData["_id"] = doc.Ref.ID
		for k, v := range data {
			docData[k] = v
		}

		documents = append(documents, docData)
		count++
	}

	return documents, count, nil
}
