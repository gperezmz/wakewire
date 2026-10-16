package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShapeFindsGoneAndRetypedFields(t *testing.T) {
	rec := `{"a":1,"b":"x","c":[{"d":true}],"e":null,"f":{"g":"h"}}`
	live := `{"a":"1","c":[{"d":true,"z":1}],"e":{"x":1},"f":{"g":"h"},"extra":1}`
	got := strings.Join(Shape([]byte(rec), []byte(live)), "; ")
	if got != "$.a: recorded number, live string; $.b: gone" {
		t.Fatal(got)
	}
}

func TestScrubberRemovesPeopleAndKeepsShape(t *testing.T) {
	s := Scrubber{Replace: [][2]string{{"gperezmz", "wwuser"}}, Drop: []string{"name"}}
	got := string(s.Body([]byte(`{"login":"gperezmz","name":"Jane","email":"g@x.com","url":"https://x/gperezmz/r","n":12345678901}`)))
	want := `{"email":"person@example.com","login":"wwuser","n":12345678901,"url":"https://x/wwuser/r"}`
	if got != want {
		t.Fatal(got)
	}
}

func TestServeReplaysInOrderAndFailsOnStrangers(t *testing.T) {
	c := Contract{Service: "x", Interactions: []Interaction{
		{Request: Request{Method: "GET", Path: "/a", Query: map[string]string{"q": "1"}}, Response: Response{Status: 200, Headers: map[string]string{"Etag": `"1"`}, Body: json.RawMessage(`[1]`)}},
		{Request: Request{Method: "GET", Path: "/a", Query: map[string]string{"q": "1"}, Headers: map[string]string{"If-None-Match": `"1"`}}, Response: Response{Status: 304}},
	}}
	srv := Serve(t, c)
	get := func(etag string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/a?q=1", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if a, b := get(""), get(`"1"`); a != 200 || b != 304 {
		t.Fatalf("%d %d", a, b)
	}
}

func TestRecorderKeepsNoAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"e"`)
		_, _ = w.Write([]byte(`{"login":"gperezmz"}`))
	}))
	defer srv.Close()
	rec := &Recorder{Next: http.DefaultTransport, Scrub: Scrubber{Replace: [][2]string{{"gperezmz", "wwuser"}},
		KeepRequest: []string{"If-None-Match"}, KeepResponse: []string{"Etag"}}}
	req, _ := http.NewRequest("GET", srv.URL+"/users/gperezmz?x=gperezmz", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, err := (&http.Client{Transport: rec}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	b, _ := json.Marshal(rec.Contract("x", ""))
	if strings.Contains(string(b), "sekrit") || strings.Contains(string(b), "gperezmz") || !strings.Contains(string(b), "/users/wwuser") {
		t.Fatal(string(b))
	}
}
