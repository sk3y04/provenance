package extractor

import (
	"encoding/json"
	"testing"
)

func TestRedditSubredditFixture(t *testing.T) {
	data := loadTestFixture(t, "reddit_subreddit.json")

	var resp rdResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal reddit response: %v", err)
	}

	if len(resp.Data.Children) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(resp.Data.Children))
	}

	post1 := resp.Data.Children[0].Data
	if post1.ID != "post1" {
		t.Errorf("expected post1, got %q", post1.ID)
	}
	if post1.Author != "testuser" {
		t.Errorf("expected testuser, got %q", post1.Author)
	}
	if post1.Subreddit != "wallpapers" {
		t.Errorf("expected wallpapers, got %q", post1.Subreddit)
	}
	if post1.Domain != "i.redd.it" {
		t.Errorf("expected i.redd.it domain, got %q", post1.Domain)
	}

	post2 := resp.Data.Children[1].Data
	if post2.ID != "post2" {
		t.Errorf("expected post2, got %q", post2.ID)
	}
	if post2.SelfText != "Check out my new desk setup" {
		t.Errorf("unexpected selftext: %q", post2.SelfText)
	}

	if resp.Data.After != "t3_post3" {
		t.Errorf("expected after t3_post3, got %q", resp.Data.After)
	}
}

func TestRedditUserFixture(t *testing.T) {
	data := loadTestFixture(t, "reddit_user.json")

	var resp rdResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal reddit user response: %v", err)
	}

	if len(resp.Data.Children) != 1 {
		t.Fatalf("expected 1 post, got %d", len(resp.Data.Children))
	}

	post := resp.Data.Children[0].Data
	if post.Author != "creator" {
		t.Errorf("expected creator, got %q", post.Author)
	}
	if post.ID != "sub1" {
		t.Errorf("expected sub1, got %q", post.ID)
	}
	if resp.Data.After != "" {
		t.Errorf("expected no after cursor, got %q", resp.Data.After)
	}
}
