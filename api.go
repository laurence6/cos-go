package cos

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	COSEndPoint     = "http://web.file.myqcloud.com/files/v1/%v/%v/%v" // appid, bucket name, path
	COSFileEndPoint = "http://%v-%v.file.myqcloud.com/%v"              // bucket name, appid, path
	ExpiredSeconds  = 600
	ListBoth        = "eListBoth"
	ListFileOnly    = "eListFileOnly"
	ListDirOnly     = "eListDirOnly"
)

type Config struct {
	Appid     string
	SecretID  string
	SecretKey string
}

type Cos struct {
	Config
	ExpiredSeconds int64
	insertOnly     int
	Client         http.Client
}

type Response struct {
	HTTPCode int
	Code     int64                  `json:"code"`
	Message  string                 `json:"message"`
	Data     map[string]interface{} `json:"data"`
}

func getParams(values map[string]interface{}) string {
	params := url.Values{}
	for k, v := range values {
		params.Add(k, fmt.Sprint(v))
	}
	return "?" + params.Encode()
}

func ProcessResponse(httpResponse *http.Response) (cosResponse *Response, err error) {
	bodyBytes, _ := ioutil.ReadAll(httpResponse.Body)
	body := string(bodyBytes)
	cosResponse = &Response{HTTPCode: httpResponse.StatusCode, Data: map[string]interface{}{}}
	err = json.Unmarshal([]byte(body), cosResponse)
	if err != nil {
		return
	}
	return
}

func New(appid, secretID, secretKey string) (cos *Cos) {
	cos = &Cos{Config{appid, secretID, secretKey}, ExpiredSeconds, 1, http.Client{}}
	return
}

func (cos *Cos) getExpired() int64 {
	return time.Now().Unix() + cos.ExpiredSeconds
}

func (cos *Cos) GetResURL(bucket, path string) string {
	return fmt.Sprintf(COSEndPoint, cos.Appid, bucket, path)
}

func (cos *Cos) CreateFolder(bucket, path string) (ret *Response, err error) {
	bucket = strings.Trim(bucket, "/")
	path, _ = GetURLSafePath(NormPath(path) + "/")
	requestURL := cos.GetResURL(bucket, path)
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	data, _ := json.Marshal(map[string]string{"op": "create"})
	request, _ := http.NewRequest("POST", requestURL, bytes.NewBuffer(data))
	request.Header.Add("Authorization", sign)
	request.Header.Add("Content-Type", "application/json")
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) Upload(file io.Reader, bucket, path string) (ret *Response, err error) {
	filecontent, err := ioutil.ReadAll(file)
	if err != nil {
		return
	}
	sha := fmt.Sprintf("%x", sha1.Sum(filecontent))
	bucket = strings.Trim(bucket, "/")
	path, _ = GetURLSafePath(NormPath(path))
	requestURL := cos.GetResURL(bucket, path)
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	writer.WriteField("op", "upload")
	writer.WriteField("sha", sha)
	writer.WriteField("insertOnly", fmt.Sprint(cos.InsertOnly))
	formfile, _ := writer.CreateFormFile("filecontent", path)
	_, err = formfile.Write(filecontent)
	if err != nil {
		return
	}
	writer.Close()

	request, _ := http.NewRequest("POST", requestURL, buffer)
	request.Header.Add("Authorization", sign)
	request.Header.Add("Content-Type", writer.FormDataContentType())
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) uploadSlicePrepare(bucket, path string, fileSize int64, sha string) (ret *Response, err error) {
	requestURL := cos.GetResURL(bucket, path)
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	writer.WriteField("op", "upload_slice")
	writer.WriteField("filesize", fmt.Sprint(fileSize))
	writer.WriteField("sha", sha)
	writer.WriteField("insertOnly", fmt.Sprint(cos.InsertOnly))
	writer.Close()

	request, _ := http.NewRequest("POST", requestURL, buffer)
	request.Header.Add("Authorization", sign)
	request.Header.Add("Content-Type", writer.FormDataContentType())
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) uploadSliceData(filecontent []byte, bucket, path, session string, offset int64) (ret *Response, err error) {
	sha := fmt.Sprintf("%x", sha1.Sum(filecontent))
	requestURL := cos.GetResURL(bucket, path)
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	writer.WriteField("op", "upload_slice")
	writer.WriteField("sha", sha)
	writer.WriteField("session", session)
	writer.WriteField("offset", fmt.Sprint(offset))
	writer.WriteField("insertOnly", fmt.Sprint(cos.InsertOnly))
	formfile, _ := writer.CreateFormFile("filecontent", path)
	_, err = formfile.Write(filecontent)
	if err != nil {
		return
	}
	writer.Close()

	request, _ := http.NewRequest("POST", requestURL, buffer)
	request.Header.Add("Authorization", sign)
	request.Header.Add("Content-Type", writer.FormDataContentType())
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) UploadSlice(file io.ReadSeeker, bucket, path string) (ret *Response, err error) {
	hash := sha1.New()
	fileSize, err := io.Copy(hash, file)
	if err != nil {
		return
	}
	sha := fmt.Sprintf("%x", hash.Sum(nil))
	bucket = strings.Trim(bucket, "/")
	path, _ = GetURLSafePath(NormPath(path))

	ret, err = cos.uploadSlicePrepare(bucket, path, fileSize, sha)

	sliceBuffer := &bytes.Buffer{}
	var session string
	var offset int64
	var sliceSize int64
	for {
		if err != nil || ret.Code != 0 {
			return
		}
		data := ret.Data
		if _, ok := data["url"]; ok { //秒传命中/已传完
			return
		}

		if session == "" {
			session = data["session"].(string)
		}
		if offset == 0 {
			offset = int64(data["offset"].(float64))
		}
		if sliceSize == 0 {
			if _, ok := data["slice_size"]; ok {
				sliceSize = int64(data["slice_size"].(float64))
			}
		}
		_, err = file.Seek(offset, 0)
		if err != nil {
			return
		}
		_, err = io.CopyN(sliceBuffer, file, sliceSize)
		if err != nil && err != io.EOF {
			return
		}
		slice, _ := ioutil.ReadAll(sliceBuffer)

		ret, err = cos.uploadSliceData(slice, bucket, path, session, offset)
		sliceBuffer.Reset()
		offset = offset + sliceSize
		if offset > fileSize {
			break
		}
	}
	return
}

func (cos *Cos) delete(bucket, path string) (ret *Response, err error) {
	if path == "" || path == "/" {
		return
	}
	bucket = strings.Trim(bucket, "/")
	requestURL := cos.GetResURL(bucket, path)
	sign := cos.SignOnce(bucket, "/"+cos.Appid+"/"+bucket+"/"+path)

	data, _ := json.Marshal(map[string]string{"op": "delete"})
	request, _ := http.NewRequest("POST", requestURL, bytes.NewBuffer(data))
	request.Header.Add("Authorization", sign)
	request.Header.Add("Content-Type", "application/json")
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) DeleteFile(bucket, path string) (*Response, error) {
	path, _ = GetURLSafePath(NormPath(path))
	return cos.delete(bucket, path)
}

func (cos *Cos) DeleteFolder(bucket, path string) (*Response, error) {
	path, _ = GetURLSafePath(NormPath(path) + "/")
	return cos.delete(bucket, path)
}

func (cos *Cos) List(bucket, path string, num uint64, pattern string, order int8, context string) (ret *Response, err error) {
	bucket = strings.Trim(bucket, "/")
	path, _ = GetURLSafePath(NormPath(path) + "/")
	params := map[string]interface{}{"op": "list"}
	if num > 0 {
		params["num"] = num
	} else {
		params["num"] = 30
	}
	if pattern == "eListBoth" || pattern == "eListDirOnly" || pattern == "eListFileOnly" {
		params["pattern"] = pattern
	} else {
		params["pattern"] = "eListBoth"
	}
	if order == 0 || order == 1 {
		params["order"] = order
	} else {
		params["order"] = 0
	}
	if context != "" {
		params["context"] = context
	}
	requestURL := cos.GetResURL(bucket, path) + getParams(params)
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	request, _ := http.NewRequest("GET", requestURL, nil)
	request.Header.Add("Authorization", sign)
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) stat(bucket, path string) (ret *Response, err error) {
	bucket = strings.Trim(bucket, "/")
	requestURL := cos.GetResURL(bucket, path) + getParams(map[string]interface{}{"op": "stat"})
	expired := cos.getExpired()
	sign := cos.SignMore(bucket, expired)

	request, _ := http.NewRequest("GET", requestURL, nil)
	request.Header.Add("Authorization", sign)
	response, err := cos.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	ret, err = ProcessResponse(response)
	return
}

func (cos *Cos) StatFile(bucket, path string) (*Response, error) {
	path, _ = GetURLSafePath(NormPath(path))
	return cos.stat(bucket, path)
}

func (cos *Cos) StatFolder(bucket, path string) (*Response, error) {
	path, _ = GetURLSafePath(NormPath(path) + "/")
	return cos.stat(bucket, path)
}
