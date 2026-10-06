// Endpoint keys in a URL path.
//
// A key is slug(group)_slug(name) from config/key/key.go, and that sanitiser
// does NOT strip ":" or "\". The SMB rows therefore have keys like
// "l:_smb-shares", because their group is a bare drive letter (a UNC path in
// the group would put backslashes in a URL path, which browsers normalise to
// "/").
//
// Gatus reads the raw path parameter on every route that validates a key
// against config.yaml: /v1/smb/:key, /v1/hv/:key, /v1/monitoring/:key,
// /v1/endpoints/:key/target and /v1/endpoints/:key/external. It never
// unescapes, so an encodeURIComponent colon arrives as %3A, matches no
// configured endpoint, and the request 404s. Storage-backed routes tolerate it,
// which is what makes the inconsistency easy to miss.
//
// A colon is legal in a path segment, so it is left alone and everything else
// is encoded as usual.
export const keyPath = (key) => encodeURIComponent(String(key || '')).replace(/%3A/gi, ':')
