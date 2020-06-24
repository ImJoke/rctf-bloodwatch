package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

const MaxInflightReqs = 10

type RctfClient struct {
	url url.URL
}

func (c *RctfClient) UserGetName(id string) string {
	epURL, err := url.Parse(fmt.Sprintf("api/v1/users/%s", id))
	if err != nil {
		panic(err.Error())
	}
	var respRaw interface{}
	r, err := http.Get(c.url.ResolveReference(epURL).String())
	if err != nil {
		panic(err.Error())
	}
	json.NewDecoder(r.Body).Decode(&respRaw)
	resp := respRaw.(map[string]interface{})
	if resp["kind"].(string) != "goodUserData" {
		panic(resp["kind"].(string))
	}
	return resp["data"].(map[string]interface{})["name"].(string)
}

func (c *RctfClient) ChallGetBlooder(id string) string {
	epURL, err := url.Parse(fmt.Sprintf("api/v1/challs/%s/solves?limit=1&offset=0", url.QueryEscape(id)))
	if err != nil {
		panic(err.Error())
	}
	var respRaw interface{}
	r, err := http.Get(c.url.ResolveReference(epURL).String())
	if err != nil {
		panic(err.Error())
	}
	json.NewDecoder(r.Body).Decode(&respRaw)
	resp := respRaw.(map[string]interface{})
	if resp["kind"].(string) != "goodChallengeSolves" {
		panic(resp["kind"].(string))
	}
	solves := resp["data"].(map[string]interface{})["solves"].([]interface{})
	if len(solves) > 0 {
		return solves[0].(map[string]interface{})["userId"].(string)
	} else {
		return ""
	}
}

type WatcherOptions struct {
	RctfURL        string
	Challenges     []string
	DiscordWebhook string
}

type Watcher struct {
	rctfClient         RctfClient
	unsolvedChallenges map[string]struct{}
	discordWebhook     string
}

func NewWatcher(options WatcherOptions) Watcher {
	url, err := url.Parse(options.RctfURL)
	if err != nil {
		panic(err.Error())
	}

	client := RctfClient{*url}

	unsolvedChallenges := make(map[string]struct{})

	wg := sync.WaitGroup{}
	m := sync.Mutex{}
	cc := make(chan string)
	for i := 0; i < MaxInflightReqs; i++ {
		go func() {
			wg.Add(1)
			defer wg.Done()
			for chall := range cc {
				if client.ChallGetBlooder(chall) == "" {
					m.Lock()
					unsolvedChallenges[chall] = struct{}{}
					m.Unlock()
				}
			}
		}()
	}
	for _, chall := range options.Challenges {
		cc <- chall
	}
	close(cc)
	wg.Wait()

	return Watcher{
		rctfClient:         client,
		unsolvedChallenges: unsolvedChallenges,
		discordWebhook:     options.DiscordWebhook,
	}
}

type DiscordWebhookPayload struct {
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Content   string `json:"content"`
	Embeds    []struct {
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
	} `json:"embeds"`
}

func (w *Watcher) notify(chall string, solver string) {
	solverName := w.rctfClient.UserGetName(solver)
	profileRelURL, err := url.Parse(fmt.Sprintf("profile/%s", solver))
	if err != nil {
		panic(err.Error())
	}
	solverProfileURL := w.rctfClient.url.ResolveReference(profileRelURL)
	payload := DiscordWebhookPayload{
		Content: fmt.Sprintf(
			"Congratulations to [%s](<%s>) for first :drop_of_blood: on %s!",
			solverName, solverProfileURL.String(), chall,
		),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		panic(err.Error())
	}
	http.Post(w.discordWebhook, "application/json", bytes.NewBuffer(payloadBytes))
}

func (w *Watcher) Check() {
	wg := sync.WaitGroup{}
	m := sync.Mutex{}
	cc := make(chan string)
	for i := 0; i < MaxInflightReqs; i++ {
		go func() {
			wg.Add(1)
			defer wg.Done()
			for chall := range cc {
				blooder := w.rctfClient.ChallGetBlooder(chall)
				if blooder != "" {
					m.Lock()
					delete(w.unsolvedChallenges, chall)
					m.Unlock()
					go w.notify(chall, blooder)
				}
			}
		}()
	}
	for chall := range w.unsolvedChallenges {
		cc <- chall
	}
	close(cc)
	wg.Wait()
}

func main() {
	rctfURL := pflag.String("rctf-url", os.Getenv("RCTF_URL"), "url of rCTF instance to monitor")
	challengesStr := pflag.String("challenges", "", "comma-separated list of challenge ids to monitor")
	discordWebhook := pflag.String("discord-webhook", os.Getenv("DISCORD_WEBHOOK"), "discord webhook to post to")
	intervalStr := pflag.String("interval", "1m", "interval to check")

	pflag.Parse()

	if *rctfURL == "" {
		panic("--rctf-url is required")
	}
	if *discordWebhook == "" {
		panic("--discord-webhook is required")
	}

	interval, err := time.ParseDuration(*intervalStr)
	if err != nil {
		panic(err.Error())
	}

	challenges := strings.Split(*challengesStr, ",")

	watcher := NewWatcher(WatcherOptions{
		RctfURL:        *rctfURL,
		Challenges:     challenges,
		DiscordWebhook: *discordWebhook,
	})

	logrus.Info("Startup complete")

	for {
		time.Sleep(interval)
		logrus.Info("Checking")
		watcher.Check()
	}
}
