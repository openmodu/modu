package extension

// Loader manages extension discovery and loading via interface injection.
type Loader struct {
	extensions []Extension
}

// NewLoader creates a new extension loader.
func NewLoader() *Loader {
	return &Loader{}
}

// Register adds an extension to be loaded.
func (l *Loader) Register(ext Extension) {
	l.extensions = append(l.extensions, ext)
}

// GetExtensions returns all registered extensions.
func (l *Loader) GetExtensions() []Extension {
	result := make([]Extension, len(l.extensions))
	copy(result, l.extensions)
	return result
}
