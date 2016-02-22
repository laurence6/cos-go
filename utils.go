package cos

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
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

func FormatResponse(response *Response) (ret string) {
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

func (cos *Cos) UploadFile(filePath, bucket, path string) (ret *Response, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return
	}
	fileSize := info.Size()
	if fileSize < 10*1024*1024 { // If file size < 10MB, directly upload
		ret, err = cos.Upload(file, bucket, path)
	} else {
		ret, err = cos.UploadSlice(file, bucket, path)
	}
	if err != nil {
		return
	}
	Logger.Printf("%v: %v", path, ret.Message)
	return
}

func (cos *Cos) UploadFolder(folderPath, bucket, path string) (ret []*Response, err error) {
	list, err := ioutil.ReadDir(folderPath)
	if err != nil {
		return
	}
	r, err := cos.CreateFolder(bucket, path)
	if err != nil {
		return
	}
	Logger.Printf("%v: %v", path, r.Message)
	ret = append(ret, r)
	chRet := make(chan []*Response)
	chErr := make(chan error)
	for _, i := range list {
		if i.IsDir() {
			go func(name string) {
				ret, err := cos.UploadFolder(folderPath+"/"+name, bucket, path+"/"+name)
				if err != nil {
					chErr <- err
					return
				}
				chRet <- ret
			}(i.Name())
		} else {
			go func(name string) {
				ret, err := cos.UploadFile(folderPath+"/"+name, bucket, path+"/"+name)
				if err != nil {
					chErr <- err
				}
				chRet <- []*Response{ret}
			}(i.Name())
		}
	}
	for range list {
		select {
		case r := <-chRet:
			for _, _r := range r {
				ret = append(ret, _r)
			}
		case err = <-chErr:
			return
		}
	}
	return
}

/*Scan scan specified folder or file
* depth > 0:
*     scan specified levels
* depth < 0:
*     scan recursively
 */
func (cos *Cos) Scan(bucket, path string, depth int) (ret []map[string]interface{}, err error) {
	if depth == 0 {
		return
	}
	dirs := []map[string]interface{}{}
	files := []map[string]interface{}{}
	context := ""
	for {
		response, err := cos.List(bucket, path, 100, "eListBoth", 0, context)
		if err != nil || response.Code != 0 {
			if err == nil && response.Code == -166 { // Treat as a file
				response, err := cos.StatFile(bucket, path)
				if err == nil && response.Code == 0 {
					data := response.Data
					data["path"] = path
					ret = append(ret, data)
				}
			}
			return ret, err
		}
		data := response.Data
		infos := data["infos"].([]interface{})
		for _, info := range infos {
			item := info.(map[string]interface{})
			item["path"] = path + "/" + item["name"].(string)
			if _, ok := item["sha"]; ok {
				files = append(files, item)
			} else {
				dirs = append(dirs, item)
			}
		}
		if hasMore, _ := data["has_more"].(bool); !hasMore {
			break
		}
		context = data["context"].(string)
	}
	ret = append(ret, dirs...)
	if depth != 1 {
		ch := make(chan []map[string]interface{})
		for _, d := range dirs {
			go func(path string) {
				list, err := cos.Scan(bucket, path, depth-1)
				if err != nil {
					return
				}
				ch <- list
			}(d["path"].(string))
		}
		for range dirs {
			ret = append(ret, <-ch...)
		}
		close(ch)
	}
	ret = append(ret, files...)
	return
}

func (cos *Cos) Delete(bucket, path string) (ret []*Response, err error) {
	fileList, err := cos.Scan(bucket, path, 1)
	if err != nil {
		return
	}
	chRet := make(chan []*Response)
	chErr := make(chan error)
	for _, i := range fileList {
		if _, ok := i["sha"]; ok {
			go func(name string) {
				ret, err := cos.DeleteFile(bucket, name)
				if err != nil {
					chErr <- err
					return
				}
				Logger.Printf("%v: %v", path, ret.Message)
				chRet <- []*Response{ret}
			}(i["path"].(string))
		} else {
			go func(name string) {
				ret, err := cos.Delete(bucket, name)
				if err != nil {
					chErr <- err
					return
				}
				chRet <- ret
			}(i["path"].(string))
		}
	}
	for range fileList {
		select {
		case r := <-chRet:
			for _, _r := range r {
				ret = append(ret, _r)
			}
		case err = <-chErr:
			return
		}
	}
	if len(fileList) == 1 && fileList[0]["path"] == path {
		return
	}
	r, err := cos.DeleteFolder(bucket, path)
	if err != nil {
		return
	}
	Logger.Printf("%v: %v", path, r.Message)
	ret = append(ret, r)
	return
}

func (cos *Cos) GetAccessURL(bucket, path string) string {
	return fmt.Sprintf(COSFileEndPoint, bucket, cos.Appid, path)
}

func (cos *Cos) GetAccessURLWithToken(bucket, path string, expireTime int64) string {
	expired := time.Now().Unix() + expireTime
	sign := cos.SignMore("debian", expired)
	return fmt.Sprintf("%s?sign=%s", cos.GetAccessURL(bucket, path), sign)
}

func (cos *Cos) IsBucketPublic(bucket string) (ret bool, err error) {
	response, err := cos.stat(bucket, "")
	if err != nil {
		return
	}
	if authority := response.Data["authority"].(string); authority == "eWPrivateRPublic" {
		ret = true
	} else if authority == "eWRPrivate" {
		ret = false
	}
	return
}

func (cos *Cos) DownloadFile(bucket, path, localPath string) (err error) {
	isPublic, err := cos.IsBucketPublic(bucket)
	if err != nil {
		return
	}
	URL := ""
	if isPublic {
		URL = cos.GetAccessURL(bucket, path)
	} else {
		URL = cos.GetAccessURLWithToken(bucket, path, 86400)
	}
	file, err := os.Create(localPath)
	if err != nil {
		return
	}
	defer file.Close()
	response, err := http.Get(URL)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, err = io.Copy(file, response.Body)
	if err != nil {
		return
	}
	Logger.Printf("%v: %v", localPath, "成功")
	return
}

func (cos *Cos) DownloadFolder(bucket, path, localPath string) (err error) {
	err = os.MkdirAll(localPath, 0755)
	if err != nil {
		return
	}
	fileList, err := cos.Scan(bucket, path, -1)
	for _, i := range fileList {
		if _, ok := i["sha"]; !ok {
			dstPath := strings.Replace(i["path"].(string), path, localPath, 1)
			err = os.MkdirAll(dstPath, 0755)
			if err != nil {
				return
			}
			Logger.Printf("%v: %v", dstPath, "成功")
		}
	}
	chErr := make(chan error)
	for _, i := range fileList {
		if _, ok := i["sha"]; ok {
			go func(path, dstPath string) {
				err := cos.DownloadFile(bucket, path, dstPath)
				if err != nil {
					chErr <- err
					return
				}
				chErr <- nil
			}(i["path"].(string), strings.Replace(i["path"].(string), path, localPath, 1))
		}
	}
	for range fileList {
		err = <-chErr
		if err != nil {
			return
		}
	}
	return
}

func (cos *Cos) GetSHA(bucket, path string) (ret string, err error) {
	response, err := cos.StatFile(bucket, path)
	if err != nil {
		return
	}
	sha, ok := response.Data["sha"]
	if !ok {
		return
	}
	ret = sha.(string)
	return
}
