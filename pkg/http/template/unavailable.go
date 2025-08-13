package template

func RespondUnavailable(err error) []byte {
	return []byte(`{
	  "status": 503,
	  "error": "Service Unavailable",
	  "message": "` + err.Error() + `"
	}`)
}
