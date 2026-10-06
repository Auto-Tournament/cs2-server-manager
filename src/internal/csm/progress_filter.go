package csm

import "io"

// ProgressFilter drops progress redraws from a stream: text that ends in a
// carriage return is the line rsync / SteamCMD redraws in place, which in a
// log file or cron mail becomes hundreds of copies. Whole lines pass through.
// Use it when the output is not a terminal.
type ProgressFilter struct {
	w    io.Writer
	line []byte
}

func NewProgressFilter(w io.Writer) *ProgressFilter { return &ProgressFilter{w: w} }

func (f *ProgressFilter) Write(p []byte) (int, error) {
	for _, b := range p {
		switch b {
		case '\r':
			f.line = f.line[:0]
		case '\n':
			f.line = append(f.line, '\n')
			if _, err := f.w.Write(f.line); err != nil {
				return 0, err
			}
			f.line = f.line[:0]
		default:
			f.line = append(f.line, b)
		}
	}
	return len(p), nil
}

// Flush writes a last line that had no newline.
func (f *ProgressFilter) Flush() error {
	if len(f.line) == 0 {
		return nil
	}
	_, err := f.w.Write(append(f.line, '\n'))
	f.line = f.line[:0]
	return err
}
