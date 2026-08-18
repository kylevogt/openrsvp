package server

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/yannkr/openrsvp/internal/auth"
	"github.com/yannkr/openrsvp/internal/event"
)

func TestInjectInviteLinkPreview_ReplacesTitleAndAddsOG(t *testing.T) {
	page := []byte("<!doctype html><html><head>\n<title>" + genericPageTitle + "</title>\n<meta name=\"description\" content=\"x\">\n</head></html>")
	got := string(injectInviteLinkPreview(page, "Summer BBQ"))

	if !strings.Contains(got, "<title>OpenRSVP - Summer BBQ</title>") {
		t.Fatalf("missing rewritten title in:\n%s", got)
	}
	if strings.Contains(got, genericPageTitle) {
		t.Fatalf("generic title still present in:\n%s", got)
	}
	if !strings.Contains(got, `property="og:title" content="OpenRSVP - Summer BBQ"`) {
		t.Fatalf("missing og:title in:\n%s", got)
	}
	if !strings.Contains(got, `name="twitter:title" content="OpenRSVP - Summer BBQ"`) {
		t.Fatalf("missing twitter:title in:\n%s", got)
	}
	if !strings.Contains(got, `meta name="description"`) {
		t.Fatalf("rewriter dropped surrounding markup:\n%s", got)
	}
}

func TestInjectInviteLinkPreview_EscapesHTML(t *testing.T) {
	title := `Party "Night" <script>alert(1)</script> & Friends`
	got := string(injectInviteLinkPreview(stubInvitePage(), title))
	want := html.EscapeString("OpenRSVP - " + title)

	if strings.Contains(got, "<script>") || strings.Contains(got, `content="OpenRSVP - Party "Night"`) {
		t.Fatalf("unescaped title leaked into HTML:\n%s", got)
	}
	if !strings.Contains(got, "<title>"+want+"</title>") {
		t.Fatalf("escaped title missing from <title> in:\n%s", got)
	}
	if !strings.Contains(got, `content="`+want+`"`) {
		t.Fatalf("escaped title missing from meta content in:\n%s", got)
	}
}

func TestInjectInviteLinkPreview_InsertsIntoHeadWithoutTitle(t *testing.T) {
	page := []byte("<html><head><meta charset=\"utf-8\"></head></html>")
	got := string(injectInviteLinkPreview(page, "Picnic"))
	if !strings.Contains(got, "<head>\n<title>OpenRSVP - Picnic</title>") {
		t.Fatalf("expected tags after <head> in:\n%s", got)
	}
}

func TestInviteLinkPreviewHTTP(t *testing.T) {
	srv, db := newTestServer(t)
	h := srv.http.Handler
	ctx := context.Background()

	store := auth.NewStore(db)
	org, err := store.CreateOrganizer(ctx, "host@example.com")
	if err != nil {
		t.Fatalf("create organizer: %v", err)
	}

	published, err := srv.eventService.Create(ctx, org.ID, event.CreateEventRequest{
		Title:     "Summer BBQ",
		EventDate: "2026-08-20T18:00",
		Location:  "Park",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if _, err := srv.eventService.Publish(ctx, published.ID, org.ID); err != nil {
		t.Fatalf("publish event: %v", err)
	}

	draft, err := srv.eventService.Create(ctx, org.ID, event.CreateEventRequest{
		Title:     "Secret Draft Party",
		EventDate: "2026-09-01T18:00",
		Location:  "Home",
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	t.Run("published invite uses event title", func(t *testing.T) {
		rr := doJSON(h, http.MethodGet, "/i/"+published.ShareToken, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /i/{token}: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if !strings.Contains(body, "<title>OpenRSVP - Summer BBQ</title>") {
			t.Errorf("missing event title in:\n%s", body)
		}
		if !strings.Contains(body, `property="og:title" content="OpenRSVP - Summer BBQ"`) {
			t.Errorf("missing og:title in:\n%s", body)
		}
		if strings.Contains(body, genericPageTitle) {
			t.Errorf("generic title still present in:\n%s", body)
		}
	})

	t.Run("unknown token keeps generic title", func(t *testing.T) {
		rr := doJSON(h, http.MethodGet, "/i/notarealtoken", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET unknown invite: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if !strings.Contains(body, genericPageTitle) {
			t.Errorf("expected generic title in:\n%s", body)
		}
		if strings.Contains(body, "Summer BBQ") {
			t.Errorf("unknown token leaked another event's title:\n%s", body)
		}
	})

	t.Run("HEAD is accepted like other SPA paths", func(t *testing.T) {
		rr := doJSON(h, http.MethodHead, "/i/"+published.ShareToken, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("HEAD /i/{token}: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("HEAD Content-Type: got %q, want text/html", ct)
		}
	})

	t.Run("draft invite does not leak title", func(t *testing.T) {
		rr := doJSON(h, http.MethodGet, "/i/"+draft.ShareToken, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET draft invite: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		if strings.Contains(body, "Secret Draft Party") {
			t.Errorf("draft event title leaked to crawlers:\n%s", body)
		}
		if !strings.Contains(body, genericPageTitle) {
			t.Errorf("expected generic title for draft invite in:\n%s", body)
		}
	})
}
