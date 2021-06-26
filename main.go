package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

type RctfClient struct {
	url   url.URL
	token string
}

type Challenge struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Solves int    `json:"solves"`
}

type apiResponse struct {
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type userInfo struct {
	ID   string
	Name string
}

type reqAuthLoginBody struct {
	TeamToken string `json:"teamToken"`
}

type respGoodLogin struct {
	AuthToken string `json:"authToken"`
}

type respGoodChallenges []Challenge

type respGoodChallengeSolves struct {
	Solves []struct {
		UserID   string `json:"userId"`
		UserName string `json:"userName"`
	} `json:"solves"`
}

type responseError = apiResponse

func (e *responseError) Error() string {
	return e.Kind + ": " + e.Message
}

func parseResponse(resp *http.Response, expectedKind string, data interface{}) error {
	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}

	if apiResp.Kind != expectedKind {
		err := responseError(apiResp)
		return &err
	}

	if data != nil {
		if err := json.Unmarshal(apiResp.Data, data); err != nil {
			return err
		}
	}

	return nil
}

func (c *RctfClient) Login(token string) error {
	epURL, err := url.Parse("api/v1/auth/login")
	if err != nil {
		panic(err)
	}
	reqBody, err := json.Marshal(reqAuthLoginBody{
		TeamToken: token,
	})
	if err != nil {
		panic(err)
	}
	r, err := http.Post(c.url.ResolveReference(epURL).String(), "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	var resp respGoodLogin
	if err := parseResponse(r, "goodLogin", &resp); err != nil {
		return err
	}
	c.token = resp.AuthToken
	return nil
}

func (c *RctfClient) ChallGetBlooder(id string) userInfo {
	epURL, err := url.Parse(fmt.Sprintf("api/v1/challs/%s/solves?limit=1&offset=0", url.QueryEscape(id)))
	if err != nil {
		panic(err)
	}
	r, err := http.Get(c.url.ResolveReference(epURL).String())
	if err != nil {
		panic(err)
	}
	var resp respGoodChallengeSolves
	if err := parseResponse(r, "goodChallengeSolves", &resp); err != nil {
		panic(err)
	}
	solves := resp.Solves
	if len(solves) > 0 {
		return userInfo{
			ID:   solves[0].UserID,
			Name: solves[0].UserName,
		}
	} else {
		return userInfo{}
	}
}

func (c *RctfClient) GetChalls() []Challenge {
	epURL, err := url.Parse("api/v1/challs")
	if err != nil {
		panic(err)
	}
	req, err := http.NewRequest("GET", c.url.ResolveReference(epURL).String(), nil)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	var resp respGoodChallenges
	if err := parseResponse(r, "goodChallenges", &resp); err != nil {
		var respErr *responseError
		if errors.As(err, &respErr) && respErr.Kind == "badNotStarted" {
			return []Challenge{}
		}
		panic(err)
	}
	return resp
}

type WatcherOptions struct {
	RctfURL        string
	Token          string
	DiscordWebhook string
}

type Watcher struct {
	rctfClient       RctfClient
	solvedChallenges map[string]struct{}
	discordWebhook   string
}

func NewWatcher(options WatcherOptions) Watcher {
	url, err := url.Parse(options.RctfURL)
	if err != nil {
		panic(err)
	}

	client := RctfClient{
		url: *url,
	}
	client.Login(options.Token)

	solvedChallenges := make(map[string]struct{})

	challs := client.GetChalls()
	for _, chall := range challs {
		if chall.Solves > 0 {
			solvedChallenges[chall.ID] = struct{}{}
		}
	}

	return Watcher{
		rctfClient:       client,
		solvedChallenges: solvedChallenges,
		discordWebhook:   options.DiscordWebhook,
	}
}

type DiscordWebhookPayloadEmbed struct {
	Author struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		IconURL string `json:"icon_url"`
	} `json:"author"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Fields      []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline,omitempty"`
	} `json:"fields"`
	Thumbnail struct {
		URL string `json:"url"`
	} `json:"thumbnail"`
	Image struct {
		URL string `json:"url"`
	} `json:"image"`
	Footer struct {
		Text    string `json:"text"`
		IconURL string `json:"icon_url"`
	} `json:"footer"`
}

type DiscordWebhookPayloadAllowedMentions struct {
	Parse       []string `json:"parse"`
	Roles       []string `json:"roles"`
	Users       []string `json:"users"`
	RepliedUser bool     `json:"replied_user"`
}

type DiscordWebhookPayload struct {
	Content         string                               `json:"content"`
	Username        string                               `json:"username"`
	AvatarURL       string                               `json:"avatar_url"`
	TTS             bool                                 `json:"tts"`
	Embeds          []DiscordWebhookPayloadEmbed         `json:"embeds"`
	AllowedMentions DiscordWebhookPayloadAllowedMentions `json:"allowed_mentions"`
}

func (w *Watcher) notify(chall Challenge) {
	solver := w.rctfClient.ChallGetBlooder(chall.ID)
	profileRelURL, err := url.Parse(fmt.Sprintf("profile/%s", solver.ID))
	if err != nil {
		panic(err)
	}
	solverProfileURL := w.rctfClient.url.ResolveReference(profileRelURL)
	payload := DiscordWebhookPayload{
		Content: fmt.Sprintf(
			"Congratulations to [%s](<%s>) for first :drop_of_blood: on %s!",
			solver.Name, solverProfileURL.String(), chall.Name,
		),
		AllowedMentions: DiscordWebhookPayloadAllowedMentions{
			Parse: []string{},
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	http.Post(w.discordWebhook, "application/json", bytes.NewBuffer(payloadBytes))
}

func (w *Watcher) Check() {
	challs := w.rctfClient.GetChalls()
	for _, chall := range challs {
		if chall.Solves > 0 {
			if _, ok := w.solvedChallenges[chall.ID]; !ok {
				go w.notify(chall)
				w.solvedChallenges[chall.ID] = struct{}{}
			}
		}
	}
}

func main() {
	rctfURL := pflag.String("rctf-url", os.Getenv("RCTF_URL"), "url of rCTF instance to monitor")
	token := pflag.String("token", os.Getenv("RCTF_TOKEN"), "rCTF token to fetch challenges with")
	discordWebhook := pflag.String("discord-webhook", os.Getenv("DISCORD_WEBHOOK"), "discord webhook to post to")
	intervalStr := pflag.String("interval", "1m", "interval to check")

	pflag.Parse()

	if *rctfURL == "" {
		panic("--rctf-url is required")
	}
	if *token == "" {
		panic("--token is required")
	}
	if *discordWebhook == "" {
		panic("--discord-webhook is required")
	}

	interval, err := time.ParseDuration(*intervalStr)
	if err != nil {
		panic(err)
	}

	watcher := NewWatcher(WatcherOptions{
		RctfURL:        *rctfURL,
		Token:          *token,
		DiscordWebhook: *discordWebhook,
	})

	logrus.Info("Startup complete")

	for {
		time.Sleep(interval)
		logrus.Info("Checking")
		watcher.Check()
	}
}
