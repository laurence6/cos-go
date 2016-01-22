package cos

import (
	"net/url"
	"fmt"
	"strings"
)

func NormPath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		path = "/"
	}
	return path
}

func GetURLSafePath(path string) (safePath string, err error) {
	tmpPath, err := url.Parse(path)
	if err != nil {
		return
	}
	safePath = fmt.Sprint(tmpPath)
	return
}

func FormatResponse(response *CosResponse) (ret string) {
	ret = "%v: %v: %v\n"
	httpcode := response.HTTPCode
	code := response.Code
	message := response.Message
	ret = fmt.Sprintf(ret, httpcode, code, message)
	data := response.Data
	for k, v := range data {
		ret += fmt.Sprintf("  %v: %v\n", k, v)
	}
	return
}
