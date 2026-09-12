package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"testing"
)

func TestPassthroughRequestModelJSON(t *testing.T) {
	body, err := json.Marshal(map[string]string{"model": "gpt-image-2", "prompt": "draw a cat"})
	if err != nil {
		t.Fatal(err)
	}

	model, err := passthroughRequestModel("application/json; charset=utf-8", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "gpt-image-2" {
		t.Fatalf("model = %q, want %q", model, "gpt-image-2")
	}
}

func TestPassthroughRequestModelMultipart(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "replace the background"); err != nil {
		t.Fatal(err)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="image"; filename="input.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	if _, err := part.Write(imageBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	contentType := writer.FormDataContentType()
	model, err := passthroughRequestModel(contentType, body.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "gpt-image-2" {
		t.Fatalf("model = %q, want %q", model, "gpt-image-2")
	}

	form, err := multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary()).ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("forwarded multipart body is invalid: %v", err)
	}
	defer form.RemoveAll()
	if got := string(form.File["image"][0].Header.Get("Content-Disposition")); got == "" {
		t.Fatal("image file metadata was not preserved")
	}
	if got := form.File["image"][0].Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("image content type = %q, want image/png", got)
	}
}

func TestPassthroughRequestModelMultipartMissingBoundary(t *testing.T) {
	_, err := passthroughRequestModel("multipart/form-data", []byte("not a multipart body"))
	if err == nil || err.Error() != "multipart 请求缺少 boundary" {
		t.Fatalf("error = %v, want missing boundary error", err)
	}
}

func TestPassthroughRequestModelMultipartMissingModel(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("prompt", "no model"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	model, err := passthroughRequestModel(writer.FormDataContentType(), body.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "" {
		t.Fatalf("model = %q, want empty model", model)
	}
}
