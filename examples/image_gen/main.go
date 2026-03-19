package main

import (
	"context"
	"fmt"

	genimagerepo "github.com/openmodu/modu/repos/gen_image_repo"
	genimagevo "github.com/openmodu/modu/vo/gen_image_vo"
)

const (
	BaseURL = "http://127.0.0.1:8045"
	APIKey  = "sk-5fec10a3ada64c0b808122ee2b971a5d"
)

func main() {
	ctx := context.Background()
	repo := genimagerepo.NewGeminiImageImpl(BaseURL, APIKey)

	prompt := "a beautiful sunset over mountains, highly detailed, photorealistic"

	fmt.Printf("正在生成图片...\n")
	fmt.Printf("Provider: %s\n\n", repo.Name())

	result, err := repo.Generate(ctx, &genimagevo.GenImageRequest{
		UserPrompt: prompt,
	})
	if err != nil {
		fmt.Printf("生成失败: %v\n", err)
		return
	}

	fmt.Printf("✓ 生成成功! 模型: %s\n", result.Model)

	if len(result.Images) == 0 {
		fmt.Println("没有生成图片")
		return
	}

	// 不传路径使用默认 ./images 目录
	savedFiles, err := genimagerepo.SaveAllImages(result)
	if err != nil {
		fmt.Printf("保存失败: %v\n", err)
		return
	}

	// 或指定目录
	// savedFiles, err := genimagerepo.SaveAllImages(result, "./output")

	for _, file := range savedFiles {
		sizeKB, _ := genimagerepo.GetFileSizeKB(file)
		fmt.Printf("✓ 保存: %s (%.2f KB)\n", file, sizeKB)
	}
}
