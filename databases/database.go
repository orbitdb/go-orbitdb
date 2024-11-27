package databases

import (
	"encoding/json"
	"errors"
	"fmt"
	"orbitdb/go-orbitdb/identities/identitytypes"
	"orbitdb/go-orbitdb/keystore"
	"orbitdb/go-orbitdb/oplog"
	"orbitdb/go-orbitdb/storage"
	"orbitdb/go-orbitdb/syncutils"
	"sync"
)

// Database represents the base class for all database types.
type Database struct {
	Address     string
	Name        string
	Identity    *identitytypes.Identity // Replace with your Identity type
	Meta        map[string]interface{}  // Metadata for the database
	Log         *oplog.Log              // Append-only log for data storage
	Sync        *syncutils.Sync         // Synchronization stub
	Events      chan interface{}        // Event channel for emitting updates
	taskQueue   chan func()             // Channel for sequential task execution
	stopChannel chan struct{}           // Channel for stopping background tasks
	mu          sync.Mutex              // Mutex for thread safety
}

// NewDatabase creates a new Database instance.
func NewDatabase(address, name string, identity *identitytypes.Identity, entryStorage storage.Storage, keyStore *keystore.KeyStore) (*Database, error) {
	// Validate address
	if address == "" {
		return nil, fmt.Errorf("address is required")
	}

	// Validate identity
	if identity == nil || !identitytypes.IsIdentity(identity) {
		return nil, fmt.Errorf("valid identity is required")
	}

	// Use default in-memory storage if no entryStorage is provided
	if entryStorage == nil {
		entryStorage = storage.NewMemoryStorage()
	}

	// Use default in-memory KeyStore if no keyStore is provided
	if keyStore == nil {
		keyStore = keystore.NewKeyStore(storage.NewMemoryStorage())
	}

	// Initialize the log
	log, err := oplog.NewLog(address, identity, entryStorage, keyStore)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize oplog: %w", err)
	}

	// Create the database instance
	db := &Database{
		Address:     address,
		Name:        name,
		Identity:    identity,
		Meta:        make(map[string]interface{}),
		Log:         log,
		Events:      make(chan interface{}, 100),
		taskQueue:   make(chan func(), 100),
		stopChannel: make(chan struct{}),
	}

	// Start processing the task queue
	go db.processTaskQueue()

	// Initialize Sync (using stubbed implementation)
	db.Sync, err = syncutils.NewSync(nil, log, db.Events, db.ApplyOperation, false)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize sync: %w", err)
	}

	return db, nil
}

// processTaskQueue processes tasks sequentially from the task queue.
func (db *Database) processTaskQueue() {
	for {
		select {
		case task := <-db.taskQueue:
			fmt.Println("processTaskQueue: Executing task")
			task()
		case <-db.stopChannel:
			fmt.Println("processTaskQueue: Stopping task processing")
			return
		}
	}
}

// AddOperation appends a new operation to the log.
func (db *Database) AddOperation(op interface{}) (string, error) {
	// Serialize the operation to a string
	payload, err := serializeOperation(op)
	if err != nil {
		return "", fmt.Errorf("failed to serialize operation: %w", err)
	}

	// Create a result channel for hash and error
	resultChan := make(chan struct {
		hash string
		err  error
	}, 1)

	// Define the task
	task := func() {
		var result struct {
			hash string
			err  error
		}

		// Append the operation to the log
		entry, err := db.Log.Append(payload)
		if err != nil {
			result.err = fmt.Errorf("failed to append to log: %w", err)
			resultChan <- result
			return
		}

		// Add the entry to sync
		if syncErr := db.Sync.Add(entry); syncErr != nil {
			result.err = fmt.Errorf("failed to sync entry: %w", syncErr)
			resultChan <- result
			return
		}

		// Emit the update event safely
		select {
		case db.Events <- entry:
		default:
			// Log or handle the case where Events channel is full
			fmt.Println("warning: Events channel full, event dropped")
		}

		// Return the hash
		result.hash = entry.Hash
		resultChan <- result
	}

	// Add the task to the queue
	db.taskQueue <- task

	// Wait for the task result
	result := <-resultChan
	return result.hash, result.err
}

// serializeOperation serializes the operation to a JSON string.
func serializeOperation(op interface{}) (string, error) {
	if op == nil {
		return "", errors.New("operation cannot be nil")
	}

	bytes, err := json.Marshal(op)
	if err != nil {
		return "", fmt.Errorf("failed to serialize operation: %w", err)
	}

	return string(bytes), nil
}

// Close stops the database's operations and cleans up resources.
func (db *Database) Close() error {
	close(db.stopChannel) // Stop task processing
	db.Sync.Stop()        // Stop synchronization
	db.Log.Close()        // Close the oplog
	close(db.Events)      // Close the event channel
	return nil
}

// Drop clears the database, removing all entries.
func (db *Database) Drop() error {
	// Clear the oplog
	if err := db.Log.Clear(); err != nil {
		return fmt.Errorf("failed to clear oplog: %w", err)
	}

	// Emit a drop event
	db.Events <- "drop"
	return nil
}

// ApplyOperation applies an operation received via synchronization.
func (db *Database) ApplyOperation(data []byte) {
	task := func() {
		fmt.Println("applyOperation: task started")

		// Decode the received data into an entry
		entry, err := oplog.Decode(data)
		if err != nil {
			fmt.Printf("applyOperation: failed to decode data: %v\n", err)
			return
		}
		fmt.Printf("applyOperation: data decoded, entry ID: %s\n", entry.Entry.ID)

		// Ensure entry belongs to the same log
		if entry.Entry.ID != db.Log.ID {
			fmt.Printf("applyOperation: log ID mismatch. Entry ID: %s, Log ID: %s\n", entry.Entry.ID, db.Log.ID)
			return
		}

		// Create a processed map for JoinEntry
		processed := make(map[string]bool)

		// Join the entry into the log
		if joinErr := db.Log.JoinEntry(&entry, processed); joinErr != nil {
			fmt.Printf("applyOperation: failed to join entry: %v\n", joinErr)
			return
		}
		fmt.Println("applyOperation: entry joined successfully")

		// Emit the update event safely
		select {
		case db.Events <- &entry:
			fmt.Println("applyOperation: event emitted")
		default:
			// Log or handle the case where Events channel is full
			fmt.Println("applyOperation: Events channel full, event dropped")
		}
	}

	// Add the task to the queue
	db.taskQueue <- task
}
