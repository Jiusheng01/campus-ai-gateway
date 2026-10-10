package app

import (
	"bytes"
	"mime/multipart"
	"testing"
)

func TestParseRequestEnvelopeJSON(t *testing.T) {
	got, err := parseRequestEnvelope([]byte(`{"model":"campus-smart","stream":true}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "campus-smart" || !got.Stream {
		t.Fatalf("unexpected envelope: %+v", got)
	}
}

func TestParseRequestEnvelopeMultipart(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("model", "campus-asr"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-audio-for-parser-test"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := parseRequestEnvelope(body.Bytes(), w.FormDataContentType())
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "campus-asr" {
		t.Fatalf("model=%q, want campus-asr", got.Model)
	}
}

func TestRequiresModelIncludesAdditionalServices(t *testing.T) {
	for _, path := range []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/embeddings",
		"/v1/audio/transcriptions",
	} {
		if !requiresModel(path) {
			t.Fatalf("%s should require a model", path)
		}
	}
}
