package genimagerepo

import (
	"github.com/openmodu/modu/pkg/utils"
	genimagevo "github.com/openmodu/modu/vo/gen_image_vo"
)

func SaveImage(img *genimagevo.Image, filename ...string) (string, error) {
	var path string
	if len(filename) > 0 && filename[0] != "" {
		path = filename[0]
	} else {
		path = utils.DefaultImageDir + "/img_" + utils.GetExtFromMimeType(img.MimeType)
	}
	if err := utils.SaveImageData(img.Data, path); err != nil {
		return "", err
	}
	return path, nil
}

func SaveAllImages(resp *genimagevo.GenImageResponse, dir ...string) ([]string, error) {
	var images [][]byte
	var mimeTypes []string
	for _, img := range resp.Images {
		images = append(images, img.Data)
		mimeTypes = append(mimeTypes, img.MimeType)
	}
	return utils.SaveImages(images, mimeTypes, dir...)
}

func GetFileSizeKB(filename string) (float64, error) {
	return utils.GetFileSizeKB(filename)
}
