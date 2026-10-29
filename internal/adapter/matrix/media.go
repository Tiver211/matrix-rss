package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

// Media downloads attachments from the homeserver and decrypts the ones from encrypted rooms.
// It implements usecase.MediaSource.
type Media struct {
	client *mautrix.Client
}

var _ usecase.MediaSource = (*Media)(nil)

// Media returns the attachment downloader of the bot.
func (b *Bot) Media() *Media {
	return &Media{client: b.client}
}

// Fetch resolves a reference made by attachmentOf and returns the file. Files larger than maxSize
// are refused without being read to the end.
func (m *Media) Fetch(ctx context.Context, ref string, maxSize int64) ([]byte, error) {
	var source mediaRef
	if err := json.Unmarshal([]byte(ref), &source); err != nil {
		return nil, fmt.Errorf("%w: bad reference", domain.ErrMediaUnavailable)
	}
	rawURL := source.URL
	if source.File != nil {
		rawURL = source.File.URL
	}
	uri, err := rawURL.Parse()
	if err != nil || uri.IsEmpty() {
		return nil, fmt.Errorf("%w: bad content URI", domain.ErrMediaUnavailable)
	}

	data, err := m.download(ctx, uri, maxSize)
	if err != nil {
		return nil, err
	}
	if source.File != nil {
		if err := source.File.DecryptInPlace(data); err != nil {
			return nil, fmt.Errorf("%w: cannot decrypt: %v", domain.ErrMediaUnavailable, err)
		}
	}
	return data, nil
}

func (m *Media) download(ctx context.Context, uri id.ContentURI, maxSize int64) ([]byte, error) {
	resp, err := m.client.Download(ctx, uri)
	if err != nil {
		// Servers without authenticated media only know the older endpoint.
		resp, err = m.downloadLegacy(ctx, uri)
	}
	if err != nil {
		var httpErr mautrix.HTTPError
		if errors.As(err, &httpErr) && (httpErr.Response.StatusCode == http.StatusNotFound || httpErr.Response.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: %v", domain.ErrMediaUnavailable, err)
		}
		return nil, err
	}
	defer resp.Body.Close()

	if maxSize > 0 && resp.ContentLength > maxSize {
		return nil, domain.ErrMediaTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, domain.ErrMediaTooLarge
	}
	return data, nil
}

func (m *Media) downloadLegacy(ctx context.Context, uri id.ContentURI) (*http.Response, error) {
	_, resp, err := m.client.MakeFullRequestWithResp(ctx, mautrix.FullRequest{
		Method:           http.MethodGet,
		URL:              m.client.BuildURL(mautrix.MediaURLPath{"v3", "download", uri.Homeserver, uri.FileID}),
		DontReadResponse: true,
	})
	return resp, err
}
