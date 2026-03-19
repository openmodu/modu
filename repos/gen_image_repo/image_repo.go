package genimagerepo

import (
	"context"

	genimagevo "github.com/openmodu/modu/vo/gen_image_vo"
)

type ImageGenRepo interface {
	Generate(ctx context.Context, req *genimagevo.GenImageRequest) (resp *genimagevo.GenImageResponse, err error)
	Name() string
}
