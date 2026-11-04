// Package programstatus receives OSC 7501 without retaining terminal content.
package programstatus

import (
	"encoding/base64"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const Reply = "\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\"

// Record intentionally excludes free-text msg/title. Validate those fields on
// receipt, but never put prompts or model prose into the agent metadata store.
type Record struct {
	ID        string    `json:"id,omitempty"`
	State     string    `json:"state"`
	Kind      string    `json:"kind,omitempty"`
	App       string    `json:"app,omitempty"`
	Progress  *int      `json:"progress,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Machine struct {
	Records       map[string]Record
	LastWorkingAt time.Time
}

func (m *Machine) Apply(body string, now time.Time) bool {
	r, ok := Parse(body)
	if !ok {
		return false
	}
	if m.Records == nil {
		m.Records = make(map[string]Record)
	}
	if r.State == "clear" {
		if r.ID == "" {
			m.LastWorkingAt = time.Time{}
		}
		for id := range m.Records {
			if r.ID == "" || id == r.ID || strings.HasPrefix(id, r.ID+"/") {
				delete(m.Records, id)
			}
		}
		return true
	}
	if _, exists := m.Records[r.ID]; !exists && len(m.Records) >= 64 {
		oldest := m.Snapshot()[0]
		for _, entry := range m.Records {
			if entry.UpdatedAt.Before(oldest.UpdatedAt) || entry.UpdatedAt.Equal(oldest.UpdatedAt) && entry.ID < oldest.ID {
				oldest = entry
			}
		}
		delete(m.Records, oldest.ID)
	}
	r.UpdatedAt = now
	m.Records[r.ID] = r
	if r.State == "working" {
		m.LastWorkingAt = now
	}
	return true
}

func (m *Machine) Prompt() {
	for id, r := range m.Records {
		if r.State == "working" || r.State == "blocked" || r.State == "idle" {
			delete(m.Records, id)
		}
	}
}

func (m *Machine) Snapshot() []Record {
	result := make([]Record, 0, len(m.Records))
	for _, r := range m.Records {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func Parse(body string) (Record, bool) {
	if len(body)+9 > 4096 {
		return Record{}, false
	}
	values := make(map[string]string)
	for _, pair := range strings.Split(body, ":") {
		key, value, ok := strings.Cut(pair, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || !letters(key) || !validValue(value) {
			continue
		}
		if len(key) > 16 {
			return Record{}, false
		}
		switch key {
		case "msg":
			if !validText(value, 2732, 2048) {
				return Record{}, false
			}
		case "title":
			if !validText(value, 256, 192) {
				return Record{}, false
			}
		case "id":
			if len(value) > 128 {
				return Record{}, false
			}
		case "app":
			if len(value) > 32 {
				return Record{}, false
			}
		}
		values[key] = value
	}
	r := Record{State: values["state"], ID: values["id"], App: values["app"]}
	switch r.State {
	case "idle", "working", "done", "blocked", "error", "clear":
	default:
		return Record{}, false
	}
	if len(r.ID) > 128 || strings.Count(r.ID, "/") >= 8 {
		return Record{}, false
	}
	if r.ID != "" {
		for _, segment := range strings.Split(r.ID, "/") {
			if !validSegment(segment) {
				return Record{}, false
			}
		}
	}
	if len(r.App) > 32 {
		return Record{}, false
	}
	if !validSegment(r.App) {
		r.App = ""
	}
	if !validText(values["msg"], 2732, 2048) || !validText(values["title"], 256, 192) {
		return Record{}, false
	}
	if r.State == "blocked" {
		switch values["kind"] {
		case "permission", "question", "auth":
			r.Kind = values["kind"]
		}
	}
	if r.State == "working" || r.State == "blocked" {
		p, valid := percentage(values["progress"])
		if valid {
			r.Progress = &p
		}
	}
	return r, true
}

func letters(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
func validValue(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.,+/=-", c)) {
			return false
		}
	}
	return true
}
func validSegment(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.+-", c)) {
			return false
		}
	}
	return true
}
func validText(s string, encoded, decoded int) bool {
	if len(s) > encoded {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil || len(data) > decoded || !utf8.Valid(data) {
		return false
	}
	for _, c := range string(data) {
		if c < 32 || c >= 127 && c <= 159 {
			return false
		}
	}
	return true
}
func percentage(s string) (int, bool) {
	if len(s) < 1 || len(s) > 3 {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n <= 100
}

// Stream consumes only OSC 7501. Other terminal traffic passes through byte for
// byte, including DCS passthrough. Detection replies are sent before forwarding
// any following DA query, so tmux's DA response cannot win Claude's probe race.
type Stream struct {
	Output       io.Writer
	Reply        io.Writer
	Changed      func()
	Machine      Machine
	mode         byte
	buf          []byte
	stringEscape bool
}

func (s *Stream) Write(data []byte) (int, error) {
	out := make([]byte, 0, len(data))
	flush := func() error {
		if len(out) == 0 {
			return nil
		}
		_, err := s.Output.Write(out)
		out = out[:0]
		return err
	}
	for _, c := range data {
		switch s.mode {
		case 0:
			if c == 27 {
				s.mode = 'e'
			} else {
				out = append(out, c)
			}
		case 'e':
			switch c {
			case ']':
				s.mode = 'o'
				s.buf = append(s.buf[:0], 27, ']')
			case 'P', '_', '^', 'X':
				out = append(out, 27, c)
				s.mode = 's'
				s.stringEscape = false
			case 27:
				out = append(out, 27)
			default:
				out = append(out, 27, c)
				s.mode = 0
				if c == 'c' {
					s.Machine.Records = nil
					s.Machine.LastWorkingAt = time.Time{}
					if s.Changed != nil {
						s.Changed()
					}
				}
			}
		case 's':
			out = append(out, c)
			if s.stringEscape {
				s.stringEscape = false
				if c == '\\' {
					s.mode = 0
				}
			} else {
				s.stringEscape = c == 27
			}
		case 'o':
			s.buf = append(s.buf, c)
			ended := c == 7 || len(s.buf) >= 2 && s.buf[len(s.buf)-2] == 27 && c == '\\'
			if !ended && len(s.buf) <= 4096 {
				continue
			}
			if !ended {
				out = append(out, s.buf...)
				s.buf = s.buf[:0]
				s.mode = 'd'
				s.stringEscape = c == 27
				continue
			}
			end := len(s.buf) - 1
			if c == '\\' {
				end--
			}
			body := string(s.buf[2:end])
			if payload, ok := strings.CutPrefix(body, "7501;"); ok && len(s.buf) <= 4096 {
				if err := flush(); err != nil {
					return 0, err
				}
				if payload == "?" {
					if _, err := io.WriteString(s.Reply, Reply); err != nil {
						return 0, err
					}
				} else if s.Machine.Apply(payload, time.Now()) && s.Changed != nil {
					s.Changed()
				}
			} else {
				out = append(out, s.buf...)
				if body == "133;A" || strings.HasPrefix(body, "133;A;") {
					s.Machine.Prompt()
					if s.Changed != nil {
						s.Changed()
					}
				}
			}
			s.buf = s.buf[:0]
			s.mode = 0
		case 'd': // Oversize OSC: forward, but never parse its embedded content.
			out = append(out, c)
			if c == 7 || s.stringEscape && c == '\\' {
				s.mode = 0
			}
			s.stringEscape = c == 27
		}
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *Stream) Flush() error {
	if s.mode == 'e' {
		_, err := s.Output.Write([]byte{27})
		return err
	}
	if len(s.buf) > 0 {
		_, err := s.Output.Write(s.buf)
		return err
	}
	return nil
}
