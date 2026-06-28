package common

import "sync"

// FileReadState tracks full-file reads so write can avoid stale overwrites.
type FileReadState struct {
	mu    sync.RWMutex
	files map[string]FileReadRecord
}

type FileReadRecord struct {
	Content      string
	ModTimeNanos int64
	Partial      bool
}

func NewFileReadState() *FileReadState {
	return &FileReadState{files: make(map[string]FileReadRecord)}
}

func (s *FileReadState) Record(path, content string, modTimeNanos int64, partial bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = make(map[string]FileReadRecord)
	}
	s.files[path] = FileReadRecord{
		Content:      content,
		ModTimeNanos: modTimeNanos,
		Partial:      partial,
	}
}

func (s *FileReadState) Get(path string) (FileReadRecord, bool) {
	if s == nil {
		return FileReadRecord{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.files[path]
	return record, ok
}
