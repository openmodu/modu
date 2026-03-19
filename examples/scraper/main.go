package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/openmodu/modu/repos/scraper"
)

func main() {
	// 命令行参数
	source := flag.String("source", "hn", "数据源: hn, ph, xhs, tldr, substack, twitter-trending, twitter-user, tophub, douyin-live")
	limit := flag.Int("limit", 10, "获取条数")
	format := flag.String("format", "text", "输出格式: text, markdown, json")
	twitterUser := flag.String("twitter-user", "", "Twitter 用户名 (当 source=twitter-user 时)")
	substackName := flag.String("substack", "", "Substack 名称 (当 source=substack 时)")
	tldrCategory := flag.String("tldr-category", "tech", "TLDR 类别: tech, ai, webdev, crypto, devops, founders")
	douyinURL := flag.String("douyin-url", "", "抖音直播间 URL (当 source=douyin-live 时)")
	debug := flag.Bool("debug", false, "调试模式 (显示详细信息)")
	flag.Parse()

	var items []scraper.NewsItem
	var err error

	fmt.Printf("正在爬取 %s ...\n\n", *source)

	switch *source {
	case "hn":
		items, err = scraper.ScrapeHN(*limit)
	case "ph":
		items, err = scraper.ScrapePH(*limit)
	case "xhs":
		items, err = scraper.ScrapeXHS(*limit)
	case "tldr":
		items, err = scraper.ScrapeTLDR(*tldrCategory, *limit)
	case "substack":
		if *substackName == "" {
			fmt.Println("错误: 需要指定 -substack 参数")
			os.Exit(1)
		}
		items, err = scraper.ScrapeSubstack(*substackName, *limit)
	case "twitter-trending":
		items, err = scraper.ScrapeTwitterTrending(*limit)
	case "twitter-user":
		if *twitterUser == "" {
			fmt.Println("错误: 需要指定 -twitter-user 参数")
			os.Exit(1)
		}
		items, err = scraper.ScrapeTwitterUser(*twitterUser, *limit)
	case "tophub":
		items, err = scraper.ScrapeTopHub(*limit)
	case "douyin-live":
		if *douyinURL == "" {
			fmt.Println("错误: 需要指定 -douyin-url 参数")
			os.Exit(1)
		}

		// Debug mode
		if *debug {
			fmt.Println("=== 调试模式 ===")
			err := scraper.DebugDouyinLivePage(*douyinURL)
			if err != nil {
				fmt.Printf("调试失败: %v\n", err)
				os.Exit(1)
			}
			return
		}

		// Handle douyin live separately as it returns different type
		var outputFormat scraper.OutputFormat
		switch *format {
		case "json":
			outputFormat = scraper.FormatJSON
		case "markdown":
			outputFormat = scraper.FormatMarkdown
		default:
			outputFormat = scraper.FormatText
		}

		liveInfo, err := scraper.ScrapeDouyinLive(*douyinURL)
		if err != nil {
			fmt.Printf("爬取失败: %v\n", err)
			os.Exit(1)
		}

		output, err := scraper.FormatDouyinLiveInfo(liveInfo, outputFormat)
		if err != nil {
			fmt.Printf("格式化失败: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(output)
		return
	default:
		fmt.Printf("不支持的数据源: %s\n", *source)
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("爬取失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("成功获取 %d 条数据\n\n", len(items))

	// 格式化输出
	var outputFormat scraper.OutputFormat
	switch *format {
	case "json":
		outputFormat = scraper.FormatJSON
	case "markdown":
		outputFormat = scraper.FormatMarkdown
	default:
		outputFormat = scraper.FormatText
	}

	output, err := scraper.FormatOutput(items, outputFormat)
	if err != nil {
		fmt.Printf("格式化失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)
}
