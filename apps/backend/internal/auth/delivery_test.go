package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSharedCodeTemplates(t *testing.T) {
	for _, language := range []string{"en", "fr", "he"} {
		message := codeMessage(language, "123456")
		if message.subject == "" || !strings.Contains(message.body, "123456") {
			t.Fatalf("missing %s template", language)
		}
	}
}

func TestSendGridAdapterUsesFixedEndpointAndServerKey(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.sendgrid.com/v3/mail/send" || request.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Fatal("wrong SendGrid endpoint or credential")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "pilot@example.test") || !strings.Contains(string(body), "123456") {
			t.Fatal("missing synthetic mail content")
		}
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	sender := SendGridSender{APIKey: "synthetic-key", From: "no-reply@invoyis.test", Client: client}
	if err := sender.Send(context.Background(), "pilot@example.test", "he", "123456"); err != nil {
		t.Fatal(err)
	}
}
