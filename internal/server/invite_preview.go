package server

import (
	"fmt"
	"html"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// genericPageTitle is the SPA shell title used on pages that are not a
// published invite link.
const genericPageTitle = "OpenRSVP — Beautiful Invitations, Zero Ads"

var (
	titleTagRe = regexp.MustCompile(`(?i)<title>[^<]*</title>`)
	headTagRe  = regexp.MustCompile(`(?i)<head[^>]*>`)
)

func invitePreviewTitle(eventTitle string) string {
	return "OpenRSVP - " + eventTitle
}

func invitePreviewTags(eventTitle string) string {
	escaped := html.EscapeString(invitePreviewTitle(eventTitle))
	return fmt.Sprintf(
		"<title>%s</title>\n\t\t<meta property=\"og:title\" content=\"%s\" />\n\t\t<meta name=\"twitter:title\" content=\"%s\" />",
		escaped, escaped, escaped,
	)
}

func stubInvitePage() []byte {
	return fmt.Appendf(nil, "<!doctype html><html lang=\"en\"><head>\n<title>%s</title>\n</head><body></body></html>", genericPageTitle)
}

// injectInviteLinkPreview rewrites the SPA HTML so crawlers see the event
// title. The original slice is not modified.
func injectInviteLinkPreview(page []byte, eventTitle string) []byte {
	tags := []byte(invitePreviewTags(eventTitle))
	if loc := titleTagRe.FindIndex(page); loc != nil {
		out := make([]byte, 0, len(page)-(loc[1]-loc[0])+len(tags))
		out = append(out, page[:loc[0]]...)
		out = append(out, tags...)
		out = append(out, page[loc[1]:]...)
		return out
	}
	if loc := headTagRe.FindIndex(page); loc != nil {
		out := make([]byte, 0, len(page)+len(tags)+1)
		out = append(out, page[:loc[1]]...)
		out = append(out, '\n')
		out = append(out, tags...)
		out = append(out, page[loc[1]:]...)
		return out
	}
	out := make([]byte, 0, len(tags)+len(page))
	out = append(out, tags...)
	out = append(out, page...)
	return out
}

// serveInvitePreview returns the SPA shell for /i/{token}, with the <title>
// and Open Graph tags swapped for "OpenRSVP - {event title}" when the share
// token belongs to a published event. Unpublished or unknown tokens keep the
// generic site title so drafts are not leaked to crawlers.
func (s *Server) serveInvitePreview(w http.ResponseWriter, r *http.Request, fallbackHTML []byte) {
	htmlPage := fallbackHTML
	if len(htmlPage) == 0 {
		htmlPage = stubInvitePage()
	}

	token := chi.URLParam(r, "token")
	if token != "" {
		ev, err := s.eventService.GetByShareToken(r.Context(), token)
		switch {
		case err == nil && ev != nil && ev.Status == "published" && ev.Title != "":
			htmlPage = injectInviteLinkPreview(htmlPage, ev.Title)
		case err != nil && err.Error() != "event not found":
			s.logger.Error().Err(err).Msg("invite preview: event lookup failed")
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(htmlPage)
}
