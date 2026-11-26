package domain

// Format ist das Ausgabeformat einer gerenderten Nachricht.
type Format string

const (
	FormatHTML Format = "html"
	FormatText Format = "text"
)

// Valid meldet, ob f ein bekanntes Format ist.
func (f Format) Valid() bool { return f == FormatHTML || f == FormatText }
