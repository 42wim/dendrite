package common

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/matrix-org/dendrite/clientapi/auth"
	"github.com/matrix-org/dendrite/clientapi/auth/authtypes"
	"github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/util"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/prometheus/client_golang/prometheus"
)

// MakeAuthAPI turns a util.JSONRequestHandler function into an http.Handler which checks the access token in the request.
func MakeAuthAPI(metricsName string, deviceDB auth.DeviceDatabase, f func(*http.Request, *authtypes.Device) util.JSONResponse) http.Handler {
	h := func(req *http.Request) util.JSONResponse {
		device, resErr := auth.VerifyAccessToken(req, deviceDB)
		if resErr != nil {
			return *resErr
		}
		return f(req, device)
	}
	return MakeExternalAPI(metricsName, h)
}

// MakeExternalAPI turns a util.JSONRequestHandler function into an http.Handler.
// This is used for APIs that are called from the internet.
func MakeExternalAPI(metricsName string, f func(*http.Request) util.JSONResponse) http.Handler {
	h := util.MakeJSONAPI(util.NewJSONRequestHandler(f))
	withSpan := func(w http.ResponseWriter, req *http.Request) {
		span := opentracing.StartSpan(metricsName)
		defer span.Finish()
		req = req.WithContext(opentracing.ContextWithSpan(req.Context(), span))
		h.ServeHTTP(w, req)
	}

	return prometheus.InstrumentHandler(metricsName, http.HandlerFunc(withSpan))
}

// MakeInternalAPI turns a util.JSONRequestHandler function into an http.Handler.
// This is used for APIs that are internal to dendrite.
// If we are passed a tracing context in the request headers then we use that
// as the parent of any tracing spans we create.
func MakeInternalAPI(metricsName string, f func(*http.Request) util.JSONResponse) http.Handler {
	h := util.MakeJSONAPI(util.NewJSONRequestHandler(f))
	withSpan := func(w http.ResponseWriter, req *http.Request) {
		carrier := opentracing.HTTPHeadersCarrier(req.Header)
		tracer := opentracing.GlobalTracer()
		clientContext, err := tracer.Extract(opentracing.HTTPHeaders, carrier)
		var span opentracing.Span
		if err == nil {
			// Default to a span without RPC context.
			span = tracer.StartSpan(metricsName)
		} else {
			// Set the RPC context.
			span = tracer.StartSpan(metricsName, ext.RPCServerOption(clientContext))
		}
		defer span.Finish()
		req = req.WithContext(opentracing.ContextWithSpan(req.Context(), span))
		h.ServeHTTP(w, req)
	}

	return prometheus.InstrumentHandler(metricsName, http.HandlerFunc(withSpan))
}

// MakeFedAPI makes an http.Handler that checks matrix federation authentication.
func MakeFedAPI(
	metricsName string,
	serverName gomatrixserverlib.ServerName,
	keyRing gomatrixserverlib.KeyRing,
	f func(*http.Request, *gomatrixserverlib.FederationRequest) util.JSONResponse,
) http.Handler {
	h := func(req *http.Request) util.JSONResponse {
		fedReq, errResp := gomatrixserverlib.VerifyHTTPRequest(
			req, time.Now(), serverName, keyRing,
		)
		if fedReq == nil {
			return errResp
		}
		return f(req, fedReq)
	}
	return MakeExternalAPI(metricsName, h)
}

// SetupHTTPAPI registers an HTTP API mux under /api and sets up a metrics
// listener.
func SetupHTTPAPI(servMux *http.ServeMux, apiMux *mux.Router) {
	// This is deprecated.
	servMux.Handle("/metrics", prometheus.Handler()) // nolint: megacheck, staticcheck
	servMux.Handle("/api/", http.StripPrefix("/api", apiMux))
}
