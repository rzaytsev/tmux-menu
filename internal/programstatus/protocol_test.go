package programstatus

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStreamFragmentationAndProbeOrdering(t *testing.T) {
	for _, chunk := range []int{1, 2, 7, 1024} {
		var output, reply bytes.Buffer
		stream := &Stream{Output: &output, Reply: &reply}
		stream.Changed = func() {
			if reply.String() != Reply {
				t.Fatal("report processed before probe reply")
			}
		}
		data := "hello\x1b]7501;?\a\x1b[c\x1b]7501;state=blocked:kind=question:app=claude-code\x1b\\world"
		for start := 0; start < len(data); start += chunk {
			if _, err := stream.Write([]byte(data[start:min(start+chunk, len(data))])); err != nil {
				t.Fatal(err)
			}
		}
		if got := output.String(); got != "hello\x1b[cworld" {
			t.Fatalf("chunk %d: output %q", chunk, got)
		}
		if got := stream.Machine.Records[""]; got.State != "blocked" || got.Kind != "question" {
			t.Fatalf("record: %+v", got)
		}
	}
}

func TestStreamDoesNotInterpretOtherControlStrings(t *testing.T) {
	data := "\x1b]2;title\a\x1bPtmux;\x1b\x1b]7501;state=error\x1b\x1b\\still-in-DCS\x1b\\\x1b_Gx\x1b]7501;state=done\a\x1b\\\x1b[31mtext\x1b[0m"
	var output, reply bytes.Buffer
	s := &Stream{Output: &output, Reply: &reply}
	for _, c := range []byte(data) {
		_, _ = s.Write([]byte{c})
	}
	if output.String() != data || len(s.Machine.Records) != 0 {
		t.Fatalf("changed non-protocol traffic: %q %+v", output.String(), s.Machine.Records)
	}
}

func TestOversizeAndIncompleteSequencesStayBounded(t *testing.T) {
	var output, reply bytes.Buffer
	s := &Stream{Output: &output, Reply: &reply}
	data := "\x1b]7501;state=working:msg=" + strings.Repeat("x", 10000) + "\x1b]7501;state=error\a"
	_, _ = s.Write([]byte(data))
	if len(s.buf) > 4096 || len(s.Machine.Records) != 0 || output.String() != data {
		t.Fatal("oversize OSC was parsed or retained")
	}
	_, _ = s.Write([]byte("\x1b]7501;state=done\a\x1b]2;unfinished"))
	_ = s.Flush()
	if s.Machine.Records[""].State != "done" || !strings.HasSuffix(output.String(), "\x1b]2;unfinished") {
		t.Fatal("failed to recover after oversize report")
	}
}

func TestValidationAndReplacement(t *testing.T) {
	for _, body := range []string{
		"state=unknown", "state=working:id=/bad", "state=working:id=a//b", "state=done:id=" + strings.Repeat("x", 33),
		"state=done:msg=Cg==", "state=done:title=/w==", "state=done:msg=abcd=", "state=done:msg=" + strings.Repeat("a", 2733),
		"state=done:" + strings.Repeat("a", 17) + "=x", "state=done:id=a/b/c/d/e/f/g/h/i",
	} {
		if _, ok := Parse(body); ok {
			t.Errorf("accepted %q", body)
		}
	}
	r, ok := Parse("bad-pair: state=working :ignored=%:state=blocked:kind=permission:msg=b2s:progress=42:app=claude-code")
	if !ok || r.State != "blocked" || r.Kind != "permission" || r.Progress == nil || *r.Progress != 42 {
		t.Fatalf("valid parsing: %+v %t", r, ok)
	}
	m := Machine{}
	m.Apply("state=blocked:kind=auth:progress=12", time.Now())
	m.Apply("state=idle", time.Now())
	if r := m.Records[""]; r.Kind != "" || r.Progress != nil {
		t.Fatal("record replacement retained omitted keys")
	}
}

func TestHierarchyLimitsAndLifetimes(t *testing.T) {
	now := time.Now()
	m := Machine{}
	for _, body := range []string{"state=idle", "state=blocked:id=a", "state=working:id=a/b", "state=done:id=ab", "state=error:id=result"} {
		m.Apply(body, now)
	}
	m.Apply("state=clear:id=a", now)
	if len(m.Records) != 3 {
		t.Fatalf("subtree clear: %+v", m.Records)
	}
	m.Prompt()
	if len(m.Records) != 2 {
		t.Fatal("prompt must retain done/error")
	}
	m.Apply("state=clear", now)
	if len(m.Records) != 0 {
		t.Fatal("root clear")
	}
	for i := 0; i < 70; i++ {
		m.Apply("state=working:id="+strings.Repeat("x", 1+i/26)+string(rune('a'+i%26)), now.Add(time.Duration(i)))
	}
	if len(m.Records) != 64 {
		t.Fatalf("record cap: %d", len(m.Records))
	}
	if _, ok := m.Records["xa"]; ok {
		t.Fatal("oldest record not evicted")
	}
}

func TestPromptAndRISAreForwarded(t *testing.T) {
	var output, reply bytes.Buffer
	s := &Stream{Output: &output, Reply: &reply}
	_, _ = s.Write([]byte("\x1b]7501;state=working\a\x1b]7501;state=done:id=result\a\x1b]133;A\x1b\\"))
	if len(s.Machine.Records) != 1 || s.Machine.Records["result"].State != "done" {
		t.Fatal("prompt lifetime")
	}
	_, _ = s.Write([]byte("\x1bc"))
	if len(s.Machine.Records) != 0 || output.String() != "\x1b]133;A\x1b\\\x1bc" {
		t.Fatal("RIS reset or output")
	}
}
