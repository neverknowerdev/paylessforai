package views

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedTemplatesRender(t *testing.T) {
	renderer, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public.html", "admin.html", "admin_login.html"} {
		response := httptest.NewRecorder()
		if err := renderer.Render(response, name, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(response.Body.String(), "<html") || response.Header().Get("Content-Type") == "" {
			t.Fatalf("template %s did not render", name)
		}
	}
}
