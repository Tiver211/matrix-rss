//go:build sqlite || !postgres

package usecase_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/greatgoodtiver/matrixrss/internal/domain"
	"github.com/greatgoodtiver/matrixrss/internal/usecase"
)

type fakeMedia struct {
	calls   atomic.Int32
	running atomic.Int32
	peak    atomic.Int32
	release chan struct{}
}

func (f *fakeMedia) Fetch(ctx context.Context, ref string, maxSize int64) ([]byte, error) {
	f.calls.Add(1)
	now := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		peak := f.peak.Load()
		if now <= peak || f.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []byte("content of " + ref), nil
}

func recordWithFile(t *testing.T, e env, id string, attachment *domain.Attachment) {
	t.Helper()
	msg := domain.Message{EventID: id, RoomID: room, Kind: domain.KindImage, Body: id, Attachment: attachment, SentAt: minute(1)}
	if err := e.messages.Record(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
}

func TestMediaOpen(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	source := &fakeMedia{}
	media := usecase.NewMediaService(e.store.Messages(), source, 1000)

	recordWithFile(t, e, "$ok", &domain.Attachment{Name: "a.png", Size: 10, Ref: "ref-ok"})
	recordWithFile(t, e, "$big", &domain.Attachment{Name: "big.bin", Size: 5000, Ref: "ref-big"})
	recordWithFile(t, e, "$noref", &domain.Attachment{Name: "x", Ref: ""})
	recordWithFile(t, e, "$none", nil)
	recordWithFile(t, e, "$gone", &domain.Attachment{Name: "g.png", Ref: "ref-gone"})
	_ = e.messages.Delete(ctx, room, "$gone", domain.Deletion{At: minute(2)})

	msg, data, err := media.Open(ctx, room, "$ok")
	if err != nil || string(data) != "content of ref-ok" || msg.Attachment.Name != "a.png" {
		t.Fatalf("open: %+v %q %v", msg, data, err)
	}
	calls := source.calls.Load()

	for id, want := range map[string]error{
		"$big":     domain.ErrMediaTooLarge,
		"$noref":   domain.ErrMediaUnavailable,
		"$none":    domain.ErrMediaUnavailable,
		"$gone":    domain.ErrMessageNotFound, // a deleted message looks like a missing one
		"$unknown": domain.ErrMessageNotFound,
	} {
		if _, _, err := media.Open(ctx, room, id); !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", id, want, err)
		}
	}
	if source.calls.Load() != calls {
		t.Error("the homeserver must not be asked for files that cannot be served")
	}
	if _, _, err := media.Open(ctx, "!other:hs", "$ok"); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Errorf("a message of another room: %v", err)
	}

	for name, tc := range map[string]struct {
		a    *domain.Attachment
		want bool
	}{
		"a normal file":     {&domain.Attachment{Size: 10, Ref: "r"}, true},
		"unknown size":      {&domain.Attachment{Ref: "r"}, true},
		"exactly the limit": {&domain.Attachment{Size: 1000, Ref: "r"}, true},
		"above the limit":   {&domain.Attachment{Size: 1001, Ref: "r"}, false},
		"no reference":      {&domain.Attachment{Size: 10}, false},
		"no attachment":     {nil, false},
	} {
		if got := media.Serves(tc.a); got != tc.want {
			t.Errorf("Serves(%s) = %v, want %v", name, got, tc.want)
		}
	}
}

func TestMediaDisabled(t *testing.T) {
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	recordWithFile(t, e, "$ok", &domain.Attachment{Ref: "ref"})

	for name, media := range map[string]*usecase.MediaService{
		"no source": usecase.NewMediaService(e.store.Messages(), nil, 1000),
		"no limit":  usecase.NewMediaService(e.store.Messages(), &fakeMedia{}, 0),
	} {
		if media.Enabled() {
			t.Errorf("%s: must be disabled", name)
		}
		if _, _, err := media.Open(context.Background(), room, "$ok"); !errors.Is(err, domain.ErrMediaUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMediaDownloadsAreBounded(t *testing.T) {
	e := newEnv(t, usecase.MessageOptions{})
	e.enable(t)
	recordWithFile(t, e, "$ok", &domain.Attachment{Ref: "ref"})
	source := &fakeMedia{release: make(chan struct{})}
	media := usecase.NewMediaService(e.store.Messages(), source, 1000)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = media.Open(context.Background(), room, "$ok")
		}()
	}
	time.Sleep(300 * time.Millisecond)
	if running := source.running.Load(); running != 4 {
		t.Fatalf("exactly 4 downloads may run at once, got %d", running)
	}
	close(source.release)
	wg.Wait()
	if peak := source.peak.Load(); peak > 4 {
		t.Fatalf("peak concurrency %d exceeds the bound", peak)
	}
	if source.calls.Load() != 12 {
		t.Fatalf("every request must finish, got %d", source.calls.Load())
	}
}
