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

func (cos *Cos) UploadFile(filePath, bucket, path string) (ret *CosResponse, err error) {
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
	return
}

func (cos *Cos) UploadFolder(folderPath, bucket, path string) (ret *CosResponse, err error) {
	list, err := ioutil.ReadDir(folderPath)
	if err != nil {
		return
	}
	ret, err = cos.CreateFolder(bucket, path)
	if err != nil {
		return
	}
	for _, i := range list {
		if i.IsDir() {
			ret, err := cos.UploadFolder(folderPath+"/"+i.Name(), bucket, path+"/"+i.Name())
			if err != nil {
				return ret, err
			}
		} else {
			ret, err := cos.UploadFile(folderPath+"/"+i.Name(), bucket, path+"/"+i.Name())
			if err != nil {
				return ret, err
			}
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
			if response.Code == -166 { // Treat as a file
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
	for _, d := range dirs {
		ret = append(ret, d)
		_list, err := cos.Scan(bucket, d["path"].(string), depth-1)
		if err != nil {
			return ret, err
		}
		ret = append(ret, _list...)
	}
	ret = append(ret, files...)
	return
}

func (cos *Cos) Delete(bucket, path string) (ret *CosResponse, err error) {
	fileList, err := cos.Scan(bucket, path, -1)
	if err != nil {
		return
	}
	for i := len(fileList) - 1; i >= 0; i-- {
		if _, ok := fileList[i]["sha"]; ok {
			ret, err = cos.DeleteFile(bucket, fileList[i]["path"].(string))
		} else {
			ret, err = cos.DeleteFolder(bucket, fileList[i]["path"].(string))
		}
		if err != nil {
			return ret, err
		}
	}
	if len(fileList) == 1 && fileList[0]["path"] == path {
		return
	}
	ret, err = cos.DeleteFolder(bucket, path)
	if err != nil {
		return
	}
	return
}

func (cos *Cos) GetAccessURL(bucket, path string) string {
	return fmt.Sprintf("http://%s-%s.file.myqcloud.com/%s", bucket, cos.Appid, path)
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
	return
}

func (cos *Cos) DownloadFolder(bucket, path, localPath string) (err error) {
	err = os.MkdirAll(localPath, 0755)
	if err != nil {
		return
	}
	fileList, err := cos.Scan(bucket, path, -1)
	for _, i := range fileList {
		dstPath := strings.Replace(i["path"].(string), path, localPath, 1)
		if _, ok := i["sha"]; ok {
			err = cos.DownloadFile(bucket, i["path"].(string), dstPath)
		} else {
			err = os.MkdirAll(dstPath, 0755)
		}
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
