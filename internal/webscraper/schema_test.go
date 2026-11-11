package webscraper

import "testing"

func TestAllowlistAcceptsExactlySevenSpiders(t *testing.T) {
	want := map[string]bool{
		"linkedin_company_information_by-url":          true,
		"linkedin_job_listings_information_by-job-url": true,
		"youtube_video-post_by-keyword":                true,
		"facebook_profile_by-profiles-url":             true,
		"youtube_video-post_by-explore":                true,
		"twitter_profile_by-profileurl":                true,
		"amazon_product_by-category-url":               true,
	}
	if got := len(AllowedSpiders()); got != len(want) {
		t.Fatalf("allowlist length = %d, want %d", got, len(want))
	}
	for id := range want {
		if _, ok := Product(id); !ok {
			t.Fatalf("allowlist does not contain %q", id)
		}
	}
	if _, ok := Product("google_product_by-url"); ok {
		t.Fatal("unsupported spider was accepted")
	}
}

func TestValidateParametersRejectsUnknownAndInvalidValues(t *testing.T) {
	sch := Schema{
		SpiderID: "amazon_product_by-category-url",
		Parameters: []Field{
			{Key: "url", Type: "string", Control: "input", Required: true},
			{Key: "page_turning", Type: "number", Control: "number", Required: true},
			{Key: "sort_by", Type: "options", Control: "select", Options: []Option{{Value: "price_low_to_high"}}},
		},
	}
	cases := []map[string]any{
		{"url": "https://amazon.com/cat", "page_turning": 0},
		{"url": "https://amazon.com/cat", "page_turning": 1, "sort_by": "unknown"},
		{"url": "https://amazon.com/cat", "page_turning": 1, "extra": "x"},
	}
	for _, input := range cases {
		if _, err := ValidateParameters(sch, input); err == nil {
			t.Fatalf("ValidateParameters(%#v) succeeded", input)
		}
	}
}

func TestValidateParametersAppliesDefaultsAndPreservesURL(t *testing.T) {
	sch := Schema{
		SpiderID: "youtube_video-post_by-keyword",
		Parameters: []Field{
			{Key: "keyword", Type: "string", Control: "input", Required: true},
			{Key: "num_of_posts", Type: "string", Control: "input", Required: false, DefaultValue: "10"},
		},
	}
	got, err := ValidateParameters(sch, map[string]any{"keyword": "a+b & c"})
	if err != nil {
		t.Fatal(err)
	}
	if got["keyword"] != "a+b & c" || got["num_of_posts"] != "10" {
		t.Fatalf("validated parameters = %#v", got)
	}
}
