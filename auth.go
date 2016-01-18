package cos

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"math/rand"
	"time"
)

func (cos *Cos) appSign(bucket, fileID string, expired int64) string {
	now := time.Now()
	rdm := rand.New(rand.NewSource(now.UnixNano())).Intn(999999999)
	plainText := []byte(fmt.Sprintf("a=%s&k=%s&e=%d&t=%d&r=%d&f=%s&b=%s",
		cos.Appid, cos.SecretID, expired, now.Unix(), rdm, fileID, bucket))
	mac := hmac.New(sha1.New, []byte(cos.SecretKey))
	mac.Write(plainText)
	signature := base64.StdEncoding.EncodeToString(append(mac.Sum(nil), plainText...))
	return signature
}

func (cos *Cos) SignOnce(bucket, fileID string) string {
	return cos.appSign(bucket, fileID, 0)
}

func (cos *Cos) SignMore(bucket string, expired int64) string {
	return cos.appSign(bucket, "", expired)
}
