package cos

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
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
func (cos *Cos) Scan(bucket, path string, depth int8) (ret []map[string]interface{}, err error) {
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
				data := response.Data
				data["path"] = path
				ret = append(ret, data)
				return ret, err
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
