package schema

// snapshotSchema 是已提交的 34 个当前引擎离线回退快照。
// 仅保留公共查询字段，确保 MCP 可离线启动；远端成功加载后始终覆盖快照。
var snapshotSchema = buildSnapshot()

func buildSnapshot() Schema {
	google := []string{"google", "google_ai_mode", "google_web", "google_shopping", "google_local", "google_videos", "google_news", "google_flights", "google_images", "google_lens", "google_trends", "google_hotels", "google_play", "google_play_product", "google_play_games", "google_play_movies", "google_play_books", "google_jobs", "google_scholar", "google_scholar_cite", "google_scholar_author", "google_maps", "google_finance", "google_finance_markets", "google_patents", "google_patents_details"}
	bing := []string{"bing", "bing_images", "bing_videos", "bing_news", "bing_maps", "bing_shopping"}
	engines := make([]Engine, 0, 34)
	for _, key := range google {
		engines = append(engines, snapshotEngine(key, snapshotQueryField(key)))
	}
	for _, key := range bing {
		engines = append(engines, snapshotEngine(key, "q"))
	}
	engines = append(engines, snapshotEngine("yandex", "text"), snapshotEngine("duckduckgo", "q"))
	return Schema{SchemaVersion: "1", Audience: "is_serp_old=0", DefaultEngine: "google", Categories: []Category{
		{Key: "google", Name: "Google", Engines: engines[:len(google)]},
		{Key: "bing", Name: "Bing", Engines: engines[len(google) : len(google)+len(bing)]},
		{Key: "yandex", Name: "Yandex", Engines: engines[len(google)+len(bing) : len(google)+len(bing)+1]},
		{Key: "duckduckgo", Name: "DuckDuckGo", Engines: engines[len(google)+len(bing)+1:]},
	}}
}

func snapshotQueryField(key string) string {
	switch key {
	case "google_flights":
		return "departure_id"
	case "google_hotels":
		return "q"
	case "google_lens":
		return "url"
	case "google_finance_markets":
		return "trend"
	case "google_patents_details":
		return "patent_id"
	case "google_scholar_author":
		return "author_id"
	default:
		return "q"
	}
}

func snapshotEngine(key, query string) Engine {
	return Engine{Key: key, Name: key, QueryField: query, Groups: []Group{{Key: "parameters", Name: "Parameters", Fields: []Field{{Key: query, Label: "Query", Type: "string", Control: "input", Visible: true, Required: true}}}}}
}
