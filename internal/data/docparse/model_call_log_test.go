package docparse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
)

// recordingObserver 是留痕观察面的替身：记录收到的载体，供行字段断言。
type recordingObserver struct {
	entries []*biz.ModelCallEntry
}

func (o *recordingObserver) ObserveModelCall(_ context.Context, entry *biz.ModelCallEntry) {
	o.entries = append(o.entries, entry)
}

// stubParseServer 起一个替身外部解析服务：含 boom 的输入按 422 拒绝，
// 其余返回固定 markdown 产物。
func stubParseServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		if strings.Contains(string(buf), "boom") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte("parse failed"))
			return
		}
		_, _ = w.Write([]byte("# parsed markdown\n\nbody paragraph."))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestModelCallLog_success_row_records_facts_digest_without_raw_bytes(t *testing.T) {
	srv := stubParseServer(t)
	obs := &recordingObserver{}
	p, cleanup, err := New(docParseConfOf(srv.URL, 0), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	body := []byte("%PDF-1.7 payload with secret content")
	ctx := biz.WithModelCallDocument(biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioParseDocument), 11)
	out, err := p.Parse(ctx, &biz.FileParseInput{MIME: mimePDF, FileName: "paper.pdf", Body: body})
	if err != nil || !strings.Contains(out, "parsed markdown") {
		t.Fatalf("Parse = (%q, %v), want markdown", out, err)
	}
	if len(obs.entries) != 1 {
		t.Fatalf("observed rows = %d, want 1", len(obs.entries))
	}
	row := obs.entries[0]
	switch {
	case row.Scenario != biz.ModelCallScenarioParseDocument:
		t.Fatalf("scenario = %q, want declared parse_document", row.Scenario)
	case row.Endpoint != srv.URL+"/parse":
		t.Fatalf("endpoint = %q, want protocol path url", row.Endpoint)
	case row.DocumentID != 11:
		t.Fatalf("document id = %d, want ctx fact", row.DocumentID)
	case row.Status != biz.ModelCallStatusSuccess || row.Error != "":
		t.Fatalf("status = (%s, %q), want success without error", row.Status, row.Error)
	case row.OutputSummary != "# parsed markdown\n\nbody paragraph.":
		t.Fatalf("output = %q, want markdown product", row.OutputSummary)
	}
	// 原件字节不落留痕：input_summary 只有事实摘要（mime/filename/size/
	// sha256），不含原件内容，也不含产物之外的原文片段。
	input := row.InputSummary
	for _, banned := range []string{"%PDF-1.7", "secret"} {
		if strings.Contains(input, banned) {
			t.Fatalf("input summary %q leaks raw file content (%q)", input, banned)
		}
	}
	for _, want := range []string{"mime=application/pdf", "filename=paper.pdf", "size=36", "sha256="} {
		if !strings.Contains(input, want) {
			t.Fatalf("input summary %q missing %s", input, want)
		}
	}
}

func TestModelCallLog_error_row_records_failure(t *testing.T) {
	srv := stubParseServer(t)
	obs := &recordingObserver{}
	p, cleanup, err := New(docParseConfOf(srv.URL, 0), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	ctx := biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioParseDocument)
	if _, err := p.Parse(ctx, &biz.FileParseInput{MIME: mimePDF, FileName: "bad.pdf", Body: []byte("boom")}); err == nil {
		t.Fatal("Parse on 422 = nil error, want failure")
	}
	row := obs.entries[0]
	switch {
	case row.Status != biz.ModelCallStatusError:
		t.Fatalf("status = %s, want error", row.Status)
	case row.Error == "":
		t.Fatalf("error = %q, want failure summary", row.Error)
	case row.OutputSummary != "":
		t.Fatalf("output = %q, want empty on failed call", row.OutputSummary)
	}
}

func TestModelCallLog_missing_scene_writes_no_row(t *testing.T) {
	// 场景缺失（调用方未声明）放弃该行：本目录无缺省回落，不伪造场景。
	srv := stubParseServer(t)
	obs := &recordingObserver{}
	p, cleanup, err := New(docParseConfOf(srv.URL, 0), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := p.Parse(context.Background(), &biz.FileParseInput{MIME: mimePDF, FileName: "x.pdf", Body: []byte("%PDF-1.7")}); err != nil {
		t.Fatalf("Parse without scene: %v", err)
	}
	if len(obs.entries) != 0 {
		t.Fatalf("observed rows = %d, want 0 (no scene declared, no forging)", len(obs.entries))
	}
}

func TestModelCallLog_unconfigured_endpoint_writes_no_row(t *testing.T) {
	// 端点未配置是调用前的配置缺失（未发生外部调用），不留行。
	obs := &recordingObserver{}
	p, cleanup, err := New(nil, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	_, err = p.Parse(biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioParseDocument),
		&biz.FileParseInput{MIME: mimePDF, FileName: "x.pdf", Body: []byte("%PDF-1.7")})
	if !errors.Is(err, biz.ErrFileTypeUnsupported) {
		t.Fatalf("Parse(unconfigured) = %v, want unsupported", err)
	}
	if len(obs.entries) != 0 {
		t.Fatalf("observed rows = %d, want 0 (no call happened)", len(obs.entries))
	}
}

func TestModelCallLog_nil_observer_zero_cost(t *testing.T) {
	srv := stubParseServer(t)
	p, cleanup, err := New(docParseConfOf(srv.URL, 0), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	out, err := p.Parse(biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioParseDocument),
		&biz.FileParseInput{MIME: mimePDF, FileName: "x.pdf", Body: []byte("%PDF-1.7")})
	if err != nil || !strings.Contains(out, "parsed markdown") {
		t.Fatalf("Parse = (%q, %v), want success with nil observer", out, err)
	}
}
