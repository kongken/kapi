package pvg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type testHTTPDoer func(req *http.Request) (*http.Response, error)

func (f testHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type memoryCache struct {
	values   map[string]string
	getCalls int
	setCalls int
	lastTTL  time.Duration
	lastKey  string
}

func (c *memoryCache) Get(_ context.Context, key string) (string, error) {
	c.getCalls++
	value, ok := c.values[key]
	if !ok {
		return "", redis.Nil
	}
	return value, nil
}

func (c *memoryCache) Set(_ context.Context, key string, value string, ttl time.Duration) error {
	c.setCalls++
	c.lastKey = key
	c.lastTTL = ttl
	if c.values == nil {
		c.values = make(map[string]string)
	}
	c.values[key] = value
	return nil
}

// envResponse builds the upstream envelope JSON around a flightList string.
func envResponse(flightList string, totalItems int, totalPages int) string {
	payload, _ := json.Marshal(flightList)
	return `{"success":true,"status":200,"data":{"pageIndex":1,"pageSize":20,"totalPages":` +
		strconv.Itoa(totalPages) + `,"totalItems":` + strconv.Itoa(totalItems) + `,"flightList":` + string(payload) + `}}`
}

const (
	// fixtureDomesticDeparture is one real domestic PVG departure (MU9007) plus
	// a share-flight record with sub-flight HTML.
	fixtureDomesticDeparture = `[
		{"peta_rn":"1","Id":"90899","航向":"出发","主航班号":"MU9007","子航班号":"","计划到达时间":"2026-08-27 08:05:00","实际到达时间":"","预计到达时间":"2026-08-27 08:05:00","计划出发时间":"2026-08-27 06:15:00","实际出发时间":"2026-08-27 06:27:00","预计出发时间":"","出发地":"上海 浦东","经停地":"","目的地":"揭阳 潮汕","候机楼":"浦东(T1)","改降机场":"","登机门状态":"","状态":"实际出发06:27","值机柜台":"A","行李传送带":"","值机区域1":"1A01","值机区域2":"1A32","值机区域3":"1A","航空公司":"中国东方航空公司","显示计划时间":"06:15","显示计划到达时间":"08:05","时间显示":"2026-08-27","具体时间":"06:15","出发地代号":"PVG","目的地代号":"SWA"},
		{"peta_rn":"2","Id":"90700","航向":"出发","主航班号":"MU6211","子航班号":"<div class='HangBan_list'><div class='List'><marquee class='marquee' direction='left' scrollamount='3' onmouseover='this.stop()' onmouseout='this.start()'><ul><li>EY5902</li><li>HO5674</li><li>MF7500</li></ul></marquee></div></div>","计划到达时间":"2026-08-27 08:54:00","实际到达时间":"","预计到达时间":"2026-08-27 08:54:00","计划出发时间":"2026-08-27 06:20:00","实际出发时间":"2026-08-27 06:25:00","预计出发时间":"","出发地":"上海 浦东","经停地":"","目的地":"银川","候机楼":"浦东(T1-S1)","改降机场":"","登机门状态":"","状态":"实际出发06:25","值机柜台":"B","行李传送带":"","值机区域1":"1B14","值机区域2":"1B26","值机区域3":"1B","航空公司":"中国东方航空公司","显示计划时间":"06:20","显示计划到达时间":"08:54","时间显示":"2026-08-27","具体时间":"06:20","出发地代号":"PVG","目的地代号":"INC"}
	]`

	// fixtureIntlDeparture is a real international PVG departure (EK303).
	fixtureIntlDeparture = `[
		{"peta_rn":"1","Id":"95501","航向":"出发","主航班号":"EK303","子航班号":"","计划到达时间":"2026-08-27 08:45:00","实际到达时间":"","预计到达时间":"2026-08-27 08:45:00","计划出发时间":"2026-08-27 00:05:00","实际出发时间":"2026-08-27 00:54:00","预计出发时间":"","出发地":"上海 浦东","经停地":"","目的地":"迪拜","候机楼":"浦东(T2)","改降机场":"","登机门状态":"","状态":"实际出发00:54","值机柜台":"C","行李传送带":"","值机区域1":"","值机区域2":"","值机区域3":"","航空公司":"阿联酋航空公司","显示计划时间":"00:05","显示计划到达时间":"08:45","时间显示":"2026-08-27","具体时间":"00:05","出发地代号":"PVG","目的地代号":"DXB"}
	]`

	// fixtureArrivalReal is a real domestic PVG arrival (HO1036A).
	fixtureArrivalReal = `[
		{"peta_rn":"1","Id":"86610","航向":"到达","主航班号":"HO1036A","子航班号":"","计划出发时间":"2026-08-25 21:50:00","实际出发时间":"2026-08-26 21:29:00","计划到达时间":"2026-08-27 00:05:00","实际到达时间":"2026-08-26 23:20:00","预计到达时间":"","出发地":"赤峰","经停地":"","目的地":"上海 浦东","候机楼":"浦东(T2)","改降机场":"","登机门状态":"","状态":"实际到达23:20","值机柜台":"","行李传送带":"44","值机区域1":"","值机区域2":"","值机区域3":"","航空公司":"吉祥航空公司","显示计划时间":"","显示计划到达时间":"00:05","时间显示":"2026-08-27","具体时间":"00:05","出发地代号":"CIF","目的地代号":"PVG"}
	]`
)

func TestUpstreamDirections(t *testing.T) {
	tests := []struct {
		direction string
		zone      string
		expected  []int
		hasErr    bool
	}{
		{"departure", "", []int{1, 3}, false},
		{"departure", "domestic", []int{1}, false},
		{"departure", "international", []int{3}, false},
		{"arrival", "", []int{2, 4}, false},
		{"arrival", "domestic", []int{2}, false},
		{"arrival", "international", []int{4}, false},
		{"sideways", "", nil, true},
		{"departure", "cargo", nil, true},
	}
	for _, tt := range tests {
		got, err := upstreamDirections(tt.direction, tt.zone)
		if tt.hasErr && err == nil {
			t.Fatalf("expected error for direction=%q zone=%q", tt.direction, tt.zone)
		}
		if !tt.hasErr && err != nil {
			t.Fatalf("unexpected error for direction=%q zone=%q: %v", tt.direction, tt.zone, err)
		}
		if !tt.hasErr && !equalInts(got, tt.expected) {
			t.Fatalf("upstreamDirections(%q,%q) = %v, want %v", tt.direction, tt.zone, got, tt.expected)
		}
	}
}

func TestZoneFromDirection(t *testing.T) {
	tests := []struct {
		direction int
		expected  string
	}{
		{1, "domestic"},
		{2, "domestic"},
		{3, "international"},
		{4, "international"},
		{9, ""},
	}
	for _, tt := range tests {
		if got := zoneFromDirection(tt.direction); got != tt.expected {
			t.Fatalf("zoneFromDirection(%d) = %q, want %q", tt.direction, got, tt.expected)
		}
	}
}

func TestParseFlightListEmpty(t *testing.T) {
	items, err := parseFlightList("")
	if err != nil {
		t.Fatalf("parseFlightList(\"\") error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}
}

func TestNormalizeResponse(t *testing.T) {
	tagged, err := parseTagged(fixtureDomesticDeparture, "domestic")
	if err != nil {
		t.Fatalf("parseTagged: %v", err)
	}
	resp := normalizeResponse("departure", Query{Direction: "departure", Zone: "domestic"}, tagged)

	if resp.Source != "shairport" || resp.Total != 2 {
		t.Fatalf("unexpected response header: %+v", resp)
	}
	// sorted by planned departure: 06:15 before 06:20
	if resp.Flights[0].FlightNumbers[0] != "MU9007" || resp.Flights[1].FlightNumbers[0] != "MU6211" {
		t.Fatalf("expected sorted flights, got %v / %v", resp.Flights[0].FlightNumbers, resp.Flights[1].FlightNumbers)
	}

	m9007 := resp.Flights[0]
	if m9007.PlannedDeparture != "06:15" || m9007.PlannedArrival != "08:05" {
		t.Fatalf("unexpected planned times: %+v", m9007)
	}
	if m9007.ActualDeparture != "06:27" {
		t.Fatalf("unexpected actual departure: %q", m9007.ActualDeparture)
	}
	if m9007.Terminal != "T1" {
		t.Fatalf("expected terminal T1, got %q", m9007.Terminal)
	}
	if m9007.DepartureAirport != "上海 浦东" || m9007.ArrivalAirport != "揭阳 潮汕" {
		t.Fatalf("unexpected airports: %+v", m9007)
	}
	if m9007.StatusText != "实际出发06:27" {
		t.Fatalf("unexpected status: %q", m9007.StatusText)
	}
	if m9007.CheckInArea != "A" || m9007.CheckInWindow != "1A01-1A32" {
		t.Fatalf("unexpected check-in fields: area=%q window=%q", m9007.CheckInArea, m9007.CheckInWindow)
	}
	if m9007.Zone != "domestic" {
		t.Fatalf("expected domestic zone, got %q", m9007.Zone)
	}
	if m9007.Raw.ID != "90899" {
		t.Fatalf("expected raw to carry upstream ID, got %+v", m9007.Raw)
	}
}

func TestSubFlightNumbersFromHTML(t *testing.T) {
	tagged, err := parseTagged(fixtureDomesticDeparture, "domestic")
	if err != nil {
		t.Fatalf("parseTagged: %v", err)
	}
	resp := normalizeResponse("departure", Query{}, tagged)

	share := resp.Flights[1]
	want := []string{"MU6211", "EY5902", "HO5674", "MF7500"}
	if len(share.FlightNumbers) != len(want) {
		t.Fatalf("expected share numbers %v, got %v", want, share.FlightNumbers)
	}
	for i := range want {
		if share.FlightNumbers[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, share.FlightNumbers)
		}
	}
	if share.Terminal != "T1-S1" {
		t.Fatalf("expected terminal T1-S1, got %q", share.Terminal)
	}
}

func TestArrivalNormalization(t *testing.T) {
	tagged, err := parseTagged(fixtureArrivalReal, "domestic")
	if err != nil {
		t.Fatalf("parseTagged: %v", err)
	}
	resp := normalizeResponse("arrival", Query{Direction: "arrival"}, tagged)

	if len(resp.Flights) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(resp.Flights))
	}
	f := resp.Flights[0]
	if f.FlightNumbers[0] != "HO1036A" {
		t.Fatalf("unexpected flight number: %v", f.FlightNumbers)
	}
	if f.PlannedArrival != "00:05" || f.ActualArrival != "23:20" {
		t.Fatalf("unexpected arrival times: %+v", f)
	}
	if f.DepartureAirport != "赤峰" || f.ArrivalAirport != "上海 浦东" {
		t.Fatalf("unexpected airports: %+v", f)
	}
	if f.BaggageBelt != "44" {
		t.Fatalf("unexpected baggage belt: %q", f.BaggageBelt)
	}
}

func TestFetchZoneMergesBothDirections(t *testing.T) {
	cache := &memoryCache{}
	var calledDirs []string
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if req.Form.Get("airCities2") != "PVG" {
			t.Fatalf("expected airCities2=PVG, got %q", req.Form.Get("airCities2"))
		}
		if req.Header.Get("User-Agent") == "" || req.Header.Get("Referer") == "" {
			t.Fatal("expected browser UA and Referer headers")
		}
		calledDirs = append(calledDirs, req.Form.Get("direction"))

		var body string
		switch req.Form.Get("direction") {
		case "1":
			body = envResponse(fixtureDomesticDeparture, 2, 1)
		case "3":
			body = envResponse(fixtureIntlDeparture, 1, 1)
		default:
			t.Fatalf("unexpected upstream direction %q", req.Form.Get("direction"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "", "", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !equalStrings(calledDirs, []string{"1", "3"}) {
		t.Fatalf("expected directions 1 and 3 called, got %v", calledDirs)
	}
	if resp.Total != 3 {
		t.Fatalf("expected merged total 3, got %d", resp.Total)
	}
	// zones carried per flight
	zones := map[string]bool{}
	for _, f := range resp.Flights {
		zones[f.Zone] = true
	}
	if !zones["domestic"] || !zones["international"] {
		t.Fatalf("expected both zones in merged result, got %v", zones)
	}
	if cache.setCalls != 1 {
		t.Fatalf("expected one cache write, got %d", cache.setCalls)
	}
}

func TestFetchZoneFiltersToSingleDirection(t *testing.T) {
	cache := &memoryCache{}
	var calledDirs []string
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		_ = req.ParseForm()
		calledDirs = append(calledDirs, req.Form.Get("direction"))
		body := envResponse(fixtureIntlDeparture, 1, 1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "international", "", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !equalStrings(calledDirs, []string{"3"}) {
		t.Fatalf("expected only direction 3 called, got %v", calledDirs)
	}
	if resp.Total != 1 || resp.Flights[0].Zone != "international" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestFetchPaginationAcrossPages(t *testing.T) {
	cache := &memoryCache{}
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		_ = req.ParseForm()
		page := req.Form.Get("currentPage")
		var body string
		switch page {
		case "1":
			body = envResponse(`[{"peta_rn":"1","主航班号":"MU1001","计划出发时间":"2026-08-27 06:00:00","候机楼":"浦东(T1)","状态":"计划"}]`, 2, 2)
		case "2":
			body = envResponse(`[{"peta_rn":"2","主航班号":"MU1002","计划出发时间":"2026-08-27 07:00:00","候机楼":"浦东(T2)","状态":"计划"}]`, 2, 2)
		default:
			t.Fatalf("unexpected page %q", page)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "domestic", "", "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("expected 2 flights across pages, got %d", resp.Total)
	}
}

func TestFetchSendsFlightNoQuery(t *testing.T) {
	cache := &memoryCache{}
	var formFlightNo, formDirection string
	var bodies []string
	client := NewClientWithCache(testHTTPDoer(func(req *http.Request) (*http.Response, error) {
		_ = req.ParseForm()
		formFlightNo = req.Form.Get("flightNum")
		formDirection = req.Form.Get("direction")
		// cross-direction query (like the official site): fetch an arrival
		// record and a departure record; only the departure one should win for
		// a departure lookup.
		fl := `[{"Id":"1","主航班号":"MU9007","航向":"到达","计划出发时间":"2026-08-26 06:15:00","候机楼":"浦东(T1)","状态":"计划"},{"Id":"2","主航班号":"MU9007","航向":"出发","计划出发时间":"2026-08-27 06:15:00","候机楼":"浦东(T1)","状态":"计划"}]`
		body := envResponse(fl, 2, 1)
		bodies = append(bodies, body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), cache, time.Minute)

	resp, err := client.Fetch(context.Background(), "departure", "", "", "MU9007")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if formFlightNo != "MU9007" {
		t.Fatalf("expected flightNum=MU9007 upstream, got %q", formFlightNo)
	}
	if formDirection != "" {
		t.Fatalf("expected empty upstream direction for flight-number query, got %q", formDirection)
	}
	if len(bodies) != 1 {
		t.Fatalf("expected exactly one upstream call for flight-number query, got %d", len(bodies))
	}
	// arrival record filtered out, departure record kept, zone unknown
	if resp.Total != 1 || resp.Flights[0].FlightNumbers[0] != "MU9007" || resp.Flights[0].Zone != "" {
		t.Fatalf("unexpected flight-number result: %+v", resp.Flights)
	}
	if resp.Flights[0].Raw.ID != "2" {
		t.Fatalf("expected the departure record (Id 2), got %+v", resp.Flights[0].Raw)
	}
}

// ---------- helpers ----------

func parseTagged(flightList string, zone string) ([]taggedFlight, error) {
	items, err := parseFlightList(flightList)
	if err != nil {
		return nil, err
	}
	out := make([]taggedFlight, 0, len(items))
	for _, item := range items {
		out = append(out, taggedFlight{upstream: item, zone: zone})
	}
	return out, nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
