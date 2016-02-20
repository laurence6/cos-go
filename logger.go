package cos

import (
	"log"
	"os"
)

var Logger = log.New(os.Stdout, "", log.LstdFlags)
