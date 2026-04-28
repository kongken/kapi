package http

import (
	"context"
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
