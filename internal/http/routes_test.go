package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kongken/kapi/internal/flight"
)

type roundTripperFunc func(req *nethttp.Request) (*nethttp.Response, error)

func (f roundTripperFunc) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return f(req)
}

func newTestHTTPClient(fn roundTripperFunc) *nethttp.Client {
	return &nethttp.Client{Transport: fn}
}

type testDailySnapshotLoader func(context.Context, string, string) ([]byte, error)

func (f testDailySnapshotLoader) Load(ctx context.Context, airportCode string, direction string) ([]byte, error) {
	return f(ctx, airportCode, direction)
}

func TestSZXDeparturesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		if req.URL.Query().Get("flag") != "D" {
			t.Fatalf("expected departure flag D, got %q", req.URL.Query().Get("flag"))
		}
		if req.URL.Query().Get("hbxx_hbh") != "CZ5387" {
			t.Fatalf("expected flight number filter, got %q", req.URL.Query().Get("hbxx_hbh"))
		}

		body := `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"16:12","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CZ5387"}],"shareflightairport":[{"imgSrc":"/app-editor/ewebeditor/uploadfile/airlineslogo/CZ.png"}],"gateCode":"324","gatedesp":"","startStationThreecharcode":"深圳","terminalStationThreecharcode":"成都双流","fltNormalStatus":"已于16:12起飞","fltNormalStatus2":"#","ckls":"A,B01-B12","fces_fcee":"14:00-15:22","apot":"T3","blls":"","craftType":"A21N"}],"type":"cn","currentDate":1,"currentTime":12,"hbxx_hbh":"CZ5387"}`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/departures?flightNo=CZ5387&currentTime=12", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"direction":"departure"`) {
		t.Fatalf("expected normalized direction, got %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"flightNumbers":["CZ5387"]`) {
		t.Fatalf("expected normalized flight numbers, got %s", recorder.Body.String())
	}
}

func TestV2AirportFlightsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		if req.URL.Query().Get("flag") != "A" {
			t.Fatalf("expected arrival flag A, got %q", req.URL.Query().Get("flag"))
		}
		if req.URL.Query().Get("type") != "en" {
			t.Fatalf("expected type=en, got %q", req.URL.Query().Get("type"))
		}

		body := `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"16:12","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CA1303"}],"shareflightairport":[{"imgSrc":"/app-editor/ewebeditor/uploadfile/airlineslogo/CA.png"}],"gateCode":"524","gatedesp":"Near lounge","startStationThreecharcode":"Beijing","terminalStationThreecharcode":"Shenzhen","fltNormalStatus":"LANDED","fltNormalStatus2":"L","ckls":"","fces_fcee":"","apot":"T3","blls":"7","craftType":"B738"}],"type":"en","currentDate":1,"currentTime":8,"hbxx_hbh":"CA1303"}`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/airports/szx/flights?direction=arrival&lang=en&flightNo=CA1303", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"airport":"szx"`) {
		t.Fatalf("expected airport code, got %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"resource":"flights"`) {
		t.Fatalf("expected flights resource, got %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"direction":"arrival"`) {
		t.Fatalf("expected arrival direction, got %s", recorder.Body.String())
	}
}

func TestV2FlightQueryRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		if req.URL.Query().Get("flag") != "D" {
			t.Fatalf("expected departure flag D, got %q", req.URL.Query().Get("flag"))
		}
		if req.URL.Query().Get("type") != "en" {
			t.Fatalf("expected type=en, got %q", req.URL.Query().Get("type"))
		}
		if req.URL.Query().Get("currentDate") != "1" {
			t.Fatalf("expected currentDate=1, got %q", req.URL.Query().Get("currentDate"))
		}
		if req.URL.Query().Get("currentTime") != "8" {
			t.Fatalf("expected currentTime=8, got %q", req.URL.Query().Get("currentTime"))
		}
		if req.URL.Query().Get("hbxx_hbh") != "CZ5387" {
			t.Fatalf("expected flight number filter, got %q", req.URL.Query().Get("hbxx_hbh"))
		}

		body := `{"flightList":[{"startSchemeTakeoffTime":"16:00","terminalSchemeLandinTime":"18:40","startRealTakeoffTime":"16:12","terminalRealLandinTime":"--:--","hbh":[{"flightNo":"CZ5387"}],"shareflightairport":[{"imgSrc":"/app-editor/ewebeditor/uploadfile/airlineslogo/CZ.png"}],"gateCode":"324","gatedesp":"","startStationThreecharcode":"Shenzhen","terminalStationThreecharcode":"Chengdu Shuangliu","fltNormalStatus":"DEPARTED","fltNormalStatus2":"D","ckls":"A,B01-B12","fces_fcee":"14:00-15:22","apot":"T3","blls":"","craftType":"A21N"}],"type":"en","currentDate":1,"currentTime":8,"hbxx_hbh":"CZ5387"}`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/flights?airport=szx&direction=departure&lang=en&date=1&time=8&flightNo=CZ5387", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"airport":"szx"`) {
		t.Fatalf("expected airport code, got %s", body)
	}
	if !strings.Contains(body, `"resource":"flights"`) {
		t.Fatalf("expected flights resource, got %s", body)
	}
	if !strings.Contains(body, `"flightNumbers":["CZ5387"]`) {
		t.Fatalf("expected normalized flight numbers, got %s", body)
	}
}

func TestV2FlightQueryRoutePassesDateToCAN(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		var upstreamRequest struct {
			Day      int    `json:"day"`
			DepOrArr string `json:"depOrArr"`
		}
		if err := json.NewDecoder(req.Body).Decode(&upstreamRequest); err != nil {
			t.Fatalf("failed to decode upstream request: %v", err)
		}
		if upstreamRequest.Day != 2 {
			t.Fatalf("expected upstream day 2, got %d", upstreamRequest.Day)
		}
		if upstreamRequest.DepOrArr != "1" {
			t.Fatalf("expected departure depOrArr 1, got %q", upstreamRequest.DepOrArr)
		}

		body := `{"code":"200","msg":"success","data":{"list":[{"flightNo":"CZ3456","flightDate":"2026-04-28","flightId":"12345","airline":"CZ","airlineCn":"南方航空","airlineEn":"China Southern","setoffTimePlan":"2026-04-28 08:30:00","setoffTimeAct":"","setoffTimePred":"","arriTimePlan":"2026-04-28 11:00:00","arriTimeAct":"","boardingTime":"","orgCityCn":"广州","orgCityEn":"Guangzhou","orgCity":"CAN","dstCityCn":"北京","dstCityEn":"Beijing","dstCity":"PEK","terminal":"T2","depTerminal":"T2","checkInCounter":"A01-A10","boardingGate":"B12","baggageTable":"","arrExit":"","flightStatusCn":"计划","flightStatusEn":"Scheduled","planeModle":"B738","depOrArr":"D","domesticOrIntl":"D","flightTask":"W/Z","isStop":0,"isShare":0,"transferCityNameCn":"","transferCityNameEn":"","shareFlight":[],"carouselFLights":[]}]}}`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/flights?airport=can&direction=departure&lang=cn&date=2", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"airport":"can"`) {
		t.Fatalf("expected airport code, got %s", body)
	}
	if !strings.Contains(body, `"date":"2"`) {
		t.Fatalf("expected response query date, got %s", body)
	}
}

func TestV2FlightQueryRouteRejectsMissingAirport(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/flights?direction=departure", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"message":"airport is required"`) {
		t.Fatalf("expected airport validation message, got %s", recorder.Body.String())
	}
}

func TestV2FlightQueryRouteRejectsUnknownAirport(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/flights?airport=pek&direction=departure", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":"airport_not_supported"`) {
		t.Fatalf("expected airport_not_supported response, got %s", recorder.Body.String())
	}
}

func TestV2AirportListRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/airports", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"code":"can"`) {
		t.Fatalf("expected can airport info, got %s", body)
	}
	if !strings.Contains(body, `"code":"szx"`) {
		t.Fatalf("expected szx airport info, got %s", body)
	}
	if !strings.Contains(body, `"nameCn":"深圳宝安国际机场"`) {
		t.Fatalf("expected szx nameCn, got %s", body)
	}
	if !strings.Contains(body, `"nameCn":"广州白云国际机场"`) {
		t.Fatalf("expected can nameCn, got %s", body)
	}
	if !strings.Contains(body, `"total":2`) {
		t.Fatalf("expected total 2, got %s", body)
	}
}

func TestV2AirportFlightsRouteRejectsUnknownAirport(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v2/airports/pek/flights?direction=departure", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":"airport_not_supported"`) {
		t.Fatalf("expected airport_not_supported response, got %s", recorder.Body.String())
	}
}

func TestSZXArrivalsRouteRejectsInvalidQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call for invalid query")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/arrivals?currentTime=bad", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":"invalid_query"`) {
		t.Fatalf("expected invalid_query response, got %s", recorder.Body.String())
	}
}

func TestSZXArrivalsRouteRejectsOutOfRangeCurrentTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call for invalid query")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/arrivals?currentTime=13", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"currentTime must be between 0 and 12"`) {
		t.Fatalf("expected currentTime range validation message, got %s", recorder.Body.String())
	}
}

func TestRoutesIncludeCORSHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected allow origin *, got %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected allow methods header")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected allow headers header")
	}
}

func TestOptionsPreflightHandledByCORS(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}))

	req := httptest.NewRequest(nethttp.MethodOptions, "/api/v1/ping", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusNoContent {
		t.Fatalf("expected status 204, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected allow origin *, got %q", got)
	}
}

func TestSZXWeatherRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	RegisterRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		if req.URL.Path != "/szjchbjk/weatherInterface/showWeather" {
			t.Fatalf("unexpected weather path %q", req.URL.Path)
		}
		if req.URL.Query().Get("callback") != "getResult" {
			t.Fatalf("expected callback=getResult, got %q", req.URL.Query().Get("callback"))
		}

		body := `getResult({"list":[{"date":"20260421","high":"30℃","low":"23℃","type":"多云间阴天，局地有（雷）阵雨，早晚有轻雾","img":"/app-editor/ewebeditor/uploadfile/weather_logo/04.png"}]})`
		return &nethttp.Response{
			StatusCode: nethttp.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(nethttp.Header),
		}, nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/weather", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"source":"szairport"`) {
		t.Fatalf("expected source in response, got %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"iconUrl":"https://www.szairport.com/app-editor/ewebeditor/uploadfile/weather_logo/04.png"`) {
		t.Fatalf("expected resolved weather icon url, got %s", recorder.Body.String())
	}
}

func TestSZXDailyDeparturesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		if airportCode != "szx" || direction != "departure" {
			t.Fatalf("unexpected daily snapshot request %s/%s", airportCode, direction)
		}
		return []byte(`{"source":"szairport","direction":"departure","query":{"currentDate":"1","currentTime":"0-12"},"total":1,"flights":[{"flightNumbers":["CZ5387"]}]}`), nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/departures/today", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"currentTime":"0-12"`) {
		t.Fatalf("expected daily response body, got %s", recorder.Body.String())
	}
}

func TestSZXDailyDeparturesRouteReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		return nil, flight.ErrDailySnapshotNotFound
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/departures/today", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSZXDelayTrendRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		if airportCode != "szx" {
			t.Fatalf("unexpected airport code %s", airportCode)
		}
		switch direction {
		case "departure":
			return []byte(`{"source":"szairport","direction":"departure","total":2,"flights":[{"plannedDepartureTime":"08:10","statusText":"延误"},{"plannedDepartureTime":"09:00","statusText":"已起飞"}]}`), nil
		case "arrival":
			return []byte(`{"source":"szairport","direction":"arrival","total":1,"flights":[{"plannedArrivalTime":"08:20","statusText":"取消"}]}`), nil
		default:
			t.Fatalf("unexpected direction %s", direction)
			return nil, nil
		}
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/delay-trend", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"resource":"delay_trend"`) {
		t.Fatalf("expected delay trend resource, got %s", body)
	}
	if !strings.Contains(body, `"timeRange":"08:00-08:59"`) {
		t.Fatalf("expected 08:00 bucket, got %s", body)
	}
	if !strings.Contains(body, `"affected":2`) {
		t.Fatalf("expected affected count, got %s", body)
	}
}

func TestSZXDelayTrendRouteReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		return nil, flight.ErrDailySnapshotNotFound
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/szx/delay-trend", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"error":"daily_snapshot_not_found"`) {
		t.Fatalf("expected not found error, got %s", recorder.Body.String())
	}
}

func TestCANDailyDeparturesRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		if airportCode != "can" || direction != "departure" {
			t.Fatalf("unexpected daily snapshot request %s/%s", airportCode, direction)
		}
		return []byte(`{"source":"baiyunairport","direction":"departure","total":1,"flights":[{"flightNumbers":["CZ3456"]}]}`), nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/can/departures/today", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"source":"baiyunairport"`) {
		t.Fatalf("expected baiyunairport source, got %s", recorder.Body.String())
	}
}

func TestCANDailyArrivalsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	registerRoutes(router, newTestHTTPClient(func(req *nethttp.Request) (*nethttp.Response, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}), testDailySnapshotLoader(func(_ context.Context, airportCode string, direction string) ([]byte, error) {
		if airportCode != "can" || direction != "arrival" {
			t.Fatalf("unexpected daily snapshot request %s/%s", airportCode, direction)
		}
		return []byte(`{"source":"baiyunairport","direction":"arrival","total":2,"flights":[{"flightNumbers":["MU5678"]},{"flightNumbers":["CA1234"]}]}`), nil
	}))

	req := httptest.NewRequest(nethttp.MethodGet, "/api/v1/can/arrivals/today", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != nethttp.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"direction":"arrival"`) {
		t.Fatalf("expected arrival direction, got %s", recorder.Body.String())
	}
}
