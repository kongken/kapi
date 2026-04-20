package szx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const weatherURL = "https://www.szairport.com/szjchbjk/weatherInterface/showWeather"

type UpstreamWeatherItem struct {
	Date string `json:"date"`
	High string `json:"high"`
	Low  string `json:"low"`
	Type string `json:"type"`
	Img  string `json:"img"`
}

type UpstreamWeatherResponse struct {
	List []UpstreamWeatherItem `json:"list"`
}

type Weather struct {
	Date    string              `json:"date"`
	High    string              `json:"high"`
	Low     string              `json:"low"`
	Type    string              `json:"type"`
	IconURL string              `json:"iconUrl"`
	Raw     UpstreamWeatherItem `json:"raw"`
}

type WeatherResponse struct {
	Source   string                  `json:"source"`
	Total    int                     `json:"total"`
	Weathers []Weather               `json:"weathers"`
	Raw      UpstreamWeatherResponse `json:"raw"`
}

func (c *Client) FetchWeather(ctx context.Context) (WeatherResponse, error) {
	upstream, err := c.fetchWeatherUpstream(ctx, true)
	if err != nil {
		return WeatherResponse{}, err
	}

	weathers := make([]Weather, 0, len(upstream.List))
	for _, item := range upstream.List {
		weathers = append(weathers, Weather{
			Date:    item.Date,
			High:    item.High,
			Low:     item.Low,
			Type:    item.Type,
			IconURL: resolveLogoURL(item.Img),
			Raw:     item,
		})
	}

	return WeatherResponse{
		Source:   "szairport",
		Total:    len(weathers),
		Weathers: weathers,
		Raw:      upstream,
	}, nil
}

func (c *Client) fetchWeatherUpstream(ctx context.Context, canRetry bool) (UpstreamWeatherResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildWeatherURL(), nil)
	if err != nil {
		return UpstreamWeatherResponse{}, fmt.Errorf("build weather request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "kapi-szx/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UpstreamWeatherResponse{}, fmt.Errorf("request weather upstream: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return UpstreamWeatherResponse{}, fmt.Errorf("read weather upstream response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return UpstreamWeatherResponse{}, fmt.Errorf("weather upstream request failed with status %d", resp.StatusCode)
	}

	payload, err := parseWeatherJSONP(body)
	if err != nil {
		if canRetry {
			retryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return c.fetchWeatherUpstream(retryCtx, false)
		}
		return UpstreamWeatherResponse{}, err
	}

	return payload, nil
}

func buildWeatherURL() string {
	params := url.Values{}
	params.Set("callback", "getResult")
	params.Set("_", fmt.Sprintf("%d", time.Now().UnixMilli()))
	return weatherURL + "?" + params.Encode()
}

func parseWeatherJSONP(body []byte) (UpstreamWeatherResponse, error) {
	content := strings.TrimSpace(string(body))
	if content == "" {
		return UpstreamWeatherResponse{}, errors.New("weather upstream returned empty body")
	}

	start := strings.Index(content, "(")
	end := strings.LastIndex(content, ")")
	if start == -1 || end == -1 || end <= start {
		return UpstreamWeatherResponse{}, errors.New("weather upstream returned invalid JSONP")
	}

	jsonPayload := strings.TrimSpace(content[start+1 : end])

	var payload UpstreamWeatherResponse
	if err := json.Unmarshal([]byte(jsonPayload), &payload); err != nil {
		return UpstreamWeatherResponse{}, errors.New("weather upstream returned invalid JSON")
	}

	return payload, nil
}
