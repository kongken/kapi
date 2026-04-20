package http

import (
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type roundTripperFunc func(req *nethttp.Request) (*nethttp.Response, error)

func (f roundTripperFunc) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return f(req)
}

func newTestHTTPClient(fn roundTripperFunc) *nethttp.Client {
	return &nethttp.Client{Transport: fn}
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
