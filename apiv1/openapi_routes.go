package apiv1

// openAPIRoutes is a work-in-progress inventory of implemented API operations.
var openAPIRoutes = []struct{ Method, Path string }{
	{"GET", "/v1/openapi.json"},
	{"GET", "/v1/info"},
	{"GET", "/v1/devices"},
	{"POST", "/v1/devices"},
	{"GET", "/v1/device-profiles"},
	{"POST", "/v1/device-profiles"},
	{"GET", "/v1/jobs"},
	{"POST", "/v1/jobs"},
	{"GET", "/v1/firmware"},
	{"POST", "/v1/firmware"},
	{"GET", "/v1/trust-anchors"},
	{"POST", "/v1/trust-anchors"},
	{"GET", "/v1/est"},
	{"PUT", "/v1/est"},
	{"GET", "/v1/models"},
	{"POST", "/v1/models"},
	{"GET", "/v1/webhooks"},
	{"POST", "/v1/webhooks"},
	{"GET", "/v1/api-keys"},
	{"POST", "/v1/api-keys"},
	{"GET", "/v1/settings"},
	{"PATCH", "/v1/settings"},
	{"GET", "/v1/audit-log"},
	{"GET", "/v1/operations"},
}
