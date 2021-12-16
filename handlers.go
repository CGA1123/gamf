package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
)

const homepage = `# gamf - GitHub App Manifest Flow

This application enables you to programmatically generate GitHub Apps by
implementing the GitHub App Manifest Flow, so that you don't have to.

## Endpoints

### POST /start

This endpoint initiates an app creation flow. You must provide it with the
following keys, encoded as JSON:

manifest    - A JSON object, acceptable by GitHub's manifest flow [1].
target_type - The account type that this GitHub App should be created on (user, org).
target_slug - The account slug to create this GitHub App on.
host        - The GitHub instance to use (usually github.com).

A JSON object containing the following keys will be returned

url - The URL to point your browser to, this will initiate the browser flow. [2]
key - A unique one-time key that you will use at the end of this flow to
      retrieve the GitHub app information.

### POST /code/:key

This endpoint returns to you the GitHub provided code to be exchanged for the
app configuration [3].

You must provide the following value as a URL parameter:

key - The key provided to you as part of the POST /start call.

A JSON object containing the following keys will be returned:

code - The GitHub App Manifest code, to be used to retrieve you new app configuration.


[1] https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#github-app-manifest-parameters
[2] https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#1-you-redirect-people-to-github-to-create-a-new-github-app
[3] https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#3-you-exchange-the-temporary-code-to-retrieve-the-app-configuration
`

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/plain")
	w.Write([]byte(homepage))
}

const donepage = `# gamf - GitHub App Manifest flow

All done, please retrieve your GitHub App configuration exchange token via POST
/code using the key provided to you via POST /start.
`

func DoneHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/plain")
	w.Write([]byte(donepage))
}

type manifestHookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

type manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     json.RawMessage   `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	CallbackURLs       []string          `json:"callback_urls"`
	Description        string            `json:"description"`
	Public             bool              `json:"public"`
	DefaultEvents      []string          `json:"default_events"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

type startRequest struct {
	Manifest   manifest `json:"manifest"`
	TargetType string   `json:"target_type"`
	TargetSlug string   `json:"target_slug"`
	Host       string   `json:"host"`
	Token      string   `json:"token"`
}

func StartHandler(baseURL string, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request startRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Add("Content-Type", "application/json")
			fmt.Printf("error: %v\n", err)
			w.Write([]byte(`{"error": "failed to parse request"}`))

			return
		}

		initialToken, err := token()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "application/json")
			w.Write([]byte(`{"error": "failed to generate a random token"}`))

			return
		}

		stateToken, err := token()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "application/json")
			w.Write([]byte(`{"error": "failed to generate a random token"}`))

			return
		}

		request.Token = stateToken
		request.Manifest.RedirectURL = fmt.Sprintf("%v/callback", baseURL)

		payload, err := json.Marshal(request)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "application/json")
			w.Write([]byte(`{"error": "failed to marshal payload for storage"}`))

			return
		}

		if err := store.SetEx(
			r.Context(),
			"i:"+initialToken,
			string(payload),
			10*time.Minute,
		); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "application/json")
			w.Write([]byte(`{"error": "failed to store payload"}`))

			return
		}

		w.Header().Add("Content-Type", "application/json")

		response := struct {
			Key string `json:"key"`
			URL string `json:"url"`
		}{
			Key: stateToken,
			URL: fmt.Sprintf("%v/redirect/%v", baseURL, initialToken),
		}

		jsonResponse, err := json.Marshal(response)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "application/json")
			w.Write([]byte(`{"error": "failed to generate response"}`))

			return

		}

		w.Write(jsonResponse)
	}
}

const redirectpage = `<html>
	<body>
		<text>Redirecting...</text>

		<form id="new-app-form" action="{{.Action}}" method="post">
			<input type="hidden" name="manifest" id="manifest" value={{.Manifest}}>
		</form>

		<script>document.getElementById("new-app-form").submit()</script>
	</body>
</html>`

type redirectTemplate struct {
	Manifest string
	Action   string
}

func RedirectHandler(store Store) http.HandlerFunc {
	tmpl := template.Must(template.New("redirect").Parse(redirectpage))

	return func(w http.ResponseWriter, r *http.Request) {
		key := mux.Vars(r)["initialKey"]

		raw, err := store.GetDel(r.Context(), "i:"+key)
		if err != nil {
			if err == redis.Nil {
				w.WriteHeader(http.StatusNotFound)
				w.Header().Add("Content-Type", "text/plain")
				w.Write([]byte("Error: failed to find metadata for the given key"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				w.Header().Add("Content-Type", "text/plain")
				w.Write([]byte("Error: failed to fetch metadata"))
			}

			return
		}

		var m startRequest
		if err := json.NewDecoder(strings.NewReader(raw)).Decode(&m); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte("Error: failed to parse metadata."))

			return
		}

		manifestJSON, err := json.Marshal(m.Manifest)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte("Error: failed to generate manifest json"))

			return
		}

		data := &redirectTemplate{
			Action:   actionURL(m),
			Manifest: string(manifestJSON),
		}

		tmpl.Execute(w, data)
	}
}

func actionURL(data startRequest) string {
	if data.TargetType == "org" {
		return fmt.Sprintf("https://%v/organizations/%v/settings/apps/new?state=%v", data.Host, data.TargetSlug, data.Token)
	} else {
		return fmt.Sprintf("https://%v/settings/apps/new?state=%v", data.Host, data.Token)
	}
}

func CallbackHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, code := r.FormValue("state"), r.FormValue("code")

		if state == "" || code == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte("Error: missing state or code parameters."))

			return
		}

		if err := store.SetEx(r.Context(), "s:"+state, code, 5*time.Minute); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Add("Content-Type", "text/plain")
			w.Write([]byte("Error: failed to store code."))

			return
		}

		http.Redirect(w, r, "/done", http.StatusFound)
	}
}

func CodeHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := mux.Vars(r)["key"]

		code, err := store.GetDel(r.Context(), "s:"+key)
		if err != nil {
			if err == redis.Nil {
				w.WriteHeader(http.StatusNotFound)
				w.Header().Add("Content-Type", "application/json")
				w.Write([]byte(`{"error": "failed to find code for the given key"}`))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				w.Header().Add("Content-Type", "application/json")
				w.Write([]byte(`"error": "failed to fetch code"}`))
			}

			return
		}

		response := fmt.Sprintf(`{"code": "%v"}`, code)

		w.Header().Add("Content-Type", "application/json")
		w.Write([]byte(response))
	}
}

func token() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("error generating token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
