package matrix

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
)

// mediaServer serves stored files like a homeserver; authenticated says whether it knows the v1 endpoint.
type mediaServer struct {
	files         map[string][]byte // "server/id" -> content
	authenticated bool
}

func (m *mediaServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const v1, v3 = "/_matrix/client/v1/media/download/", "/_matrix/media/v3/download/"
	var key string
	switch {
	case strings.HasPrefix(r.URL.Path, v1) && m.authenticated:
		key = strings.TrimPrefix(r.URL.Path, v1)
	case strings.HasPrefix(r.URL.Path, v3):
		key = strings.TrimPrefix(r.URL.Path, v3)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_UNRECOGNIZED","error":"unknown endpoint"}`))
		return
	}
	data, ok := m.files[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"no such file"}`))
		return
	}
	_, _ = w.Write(data)
}

func newMedia(t *testing.T, server *mediaServer) *Media {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	client, err := mautrix.NewClient(httpServer.URL, "@bot:hs", "token")
	if err != nil {
		t.Fatal(err)
	}
	return &Media{client: client}
}

func refOf(t *testing.T, ref mediaRef) string {
	t.Helper()
	encoded, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestFetchPlainFile(t *testing.T) {
	for _, authenticated := range []bool{true, false} { // the older endpoint is the fallback
		media := newMedia(t, &mediaServer{files: map[string][]byte{"hs/cat": []byte("meow")}, authenticated: authenticated})
		data, err := media.Fetch(t.Context(), refOf(t, mediaRef{URL: "mxc://hs/cat"}), 1000)
		if err != nil || string(data) != "meow" {
			t.Fatalf("authenticated=%v: %q, %v", authenticated, data, err)
		}
	}
}

func TestFetchEncryptedFile(t *testing.T) {
	plaintext := []byte("a secret picture")
	file := attachment.NewEncryptedFile()
	ciphertext := file.Encrypt(plaintext)
	media := newMedia(t, &mediaServer{files: map[string][]byte{"hs/enc": ciphertext}, authenticated: true})

	ref := refOf(t, mediaRef{File: &event.EncryptedFileInfo{EncryptedFile: *file, URL: "mxc://hs/enc"}})
	data, err := media.Fetch(t.Context(), ref, 1000)
	if err != nil || !bytes.Equal(data, plaintext) {
		t.Fatalf("decrypted: %q, %v", data, err)
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[0] ^= 0xff
	media = newMedia(t, &mediaServer{files: map[string][]byte{"hs/enc": tampered}, authenticated: true})
	if _, err := media.Fetch(t.Context(), ref, 1000); !errors.Is(err, domain.ErrMediaUnavailable) {
		t.Fatalf("a file with a wrong hash must be refused, got %v", err)
	}
}

func TestFetchRefusesWhatItCannotServe(t *testing.T) {
	media := newMedia(t, &mediaServer{files: map[string][]byte{"hs/big": bytes.Repeat([]byte("x"), 500)}, authenticated: true})
	ctx := t.Context()

	if _, err := media.Fetch(ctx, refOf(t, mediaRef{URL: "mxc://hs/big"}), 100); !errors.Is(err, domain.ErrMediaTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := media.Fetch(ctx, refOf(t, mediaRef{URL: "mxc://hs/gone"}), 100); !errors.Is(err, domain.ErrMediaUnavailable) {
		t.Errorf("missing on the homeserver: %v", err)
	}
	for _, bad := range []string{"", "not json", `{}`, `{"url":"not-a-uri"}`} {
		if _, err := media.Fetch(ctx, bad, 100); !errors.Is(err, domain.ErrMediaUnavailable) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
