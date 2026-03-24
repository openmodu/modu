# notebooklm

An unofficial Google NotebookLM Go SDK, supporting Notebook, Source, and Artifact management, as well as Chat functionality.

## Installation

```go
import "github.com/openmodu/modu/repos/notebooklm"
```

## Quick Start

```go
// Log in (will open a browser)
notebooklm.Login()

// Create a client from stored authentication info
client, err := notebooklm.NewClientFromStorage("")
if err != nil {
    log.Fatal(err)
}

// List all Notebooks
notebooks, err := client.ListNotebooks(ctx)

// Create a new Notebook
notebook, err := client.CreateNotebook(ctx, "My Notebook")

// Add a URL as a source
source, err := client.AddSourceURL(ctx, notebook.ID, "https://example.com/article")

// Generate an audio podcast
status, err := client.GenerateAudio(ctx, notebook.ID, vo.AudioFormatDeepDive, vo.AudioLengthDefault)

// Ask a question
result, err := client.Ask(ctx, notebook.ID, "Summarize this content", nil)
fmt.Println(result.Answer)
```

## Authentication

The first use requires logging in via a browser:

```go
// Log in and automatically save authentication info
if err := notebooklm.Login(); err != nil {
    log.Fatal(err)
}

// Subsequent use: Load from storage
client, err := notebooklm.NewClientFromStorage("")

// Check if storage exists
if notebooklm.StorageExists() {
    // Already logged in
}

// Get the storage path
path := notebooklm.GetStoragePath()
```

## API Overview

### Notebook Operations

```go
// List all Notebooks
notebooks, _ := client.ListNotebooks(ctx)

// Create a Notebook
notebook, _ := client.CreateNotebook(ctx, "Title")

// Get a Notebook
notebook, _ := client.GetNotebook(ctx, notebookID)

// Rename a Notebook
client.RenameNotebook(ctx, notebookID, "New Title")

// Delete a Notebook
client.DeleteNotebook(ctx, notebookID)
```

### Source Operations

```go
// List all Sources
sources, _ := client.ListSources(ctx, notebookID)

// Add a URL (automatically recognizes YouTube)
source, _ := client.AddSourceURL(ctx, notebookID, "https://...")

// Add a local file
source, _ := client.AddSourceFile(ctx, notebookID, "/path/to/file.pdf")

// Add text
source, _ := client.AddSourceText(ctx, notebookID, "Title", "Content...")

// Delete a Source
client.DeleteSource(ctx, notebookID, sourceID)
```

### Artifact Operations

```go
// Generate an audio podcast
status, _ := client.GenerateAudio(ctx, notebookID, vo.AudioFormatDeepDive, vo.AudioLengthDefault)

// Generate a video
status, _ := client.GenerateVideo(ctx, notebookID, vo.VideoFormatBriefing, vo.VideoStyleClassroom)

// Poll generation status
status, _ := client.PollGeneration(ctx, notebookID, status.TaskID)

// List all Artifacts
artifacts, _ := client.ListArtifacts(ctx, notebookID)

// Download audio
client.DownloadAudio(ctx, notebookID, "./output.m4a", "")

// Download video
client.DownloadVideo(ctx, notebookID, "./output.mp4", "")
```

### Chat Operations

```go
// Ask a question to the Notebook (uses all sources)
result, _ := client.Ask(ctx, notebookID, "Question content", nil)
fmt.Println(result.Answer)

// Ask with specific sources
result, _ := client.Ask(ctx, notebookID, "Question", []string{sourceID1, sourceID2})
```

## Audio/Video Format Options

```go
// Audio formats
vo.AudioFormatDeepDive      // Deep dive conversation
vo.AudioFormatConversation  // Conversation format

// Audio length
vo.AudioLengthDefault       // Default
vo.AudioLengthShort         // Short
vo.AudioLengthMedium        // Medium
vo.AudioLengthLong          // Long

// Video formats
vo.VideoFormatBriefing      // Briefing

// Video styles
vo.VideoStyleClassroom      // Classroom style
```

## File Structure

```
notebooklm/
├── client.go      # Core Client and RPC calls
├── notebook.go    # Notebook CRUD
├── source.go      # Source management
├── artifact.go    # Artifact generation/download
├── chat.go        # Chat functionality
├── utils.go       # Utility functions
├── auth.go        # Authentication management
├── login.go       # Browser login
├── parser.go      # Response parsing
└── rpc/           # RPC encoding/decoding
```
