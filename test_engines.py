import json
import urllib.request

URL = "http://localhost:8800/dbd613c046d9f5aea0c2e370107a986e/mcp"
LENS_IMAGE = "https://upload.wikimedia.org/wikipedia/commons/thumb/d/d9/Collage_of_Nine_Dogs.jpg/640px-Collage_of_Nine_Dogs.jpg"

CASES = [
    ("google", "Thordata SERP API"),
    ("google_images", "golden retriever"),
    ("google_videos", "python tutorial"),
    ("google_maps", "coffee shop in Seattle"),
    ("google_news", "artificial intelligence"),
    ("google_shopping", "wireless earbuds"),
    ("google_lens", LENS_IMAGE),
    ("google_scholar", "attention is all you need"),
    ("google_patents", "foldable smartphone"),
]


def call(engine, q):
    payload = json.dumps({
        "jsonrpc": "2.0", "id": 1, "method": "tools/call",
        "params": {"name": "search", "arguments": {"engine": engine, "q": q, "json": "1"}},
    }).encode()
    req = urllib.request.Request(URL, data=payload, headers={
        "Content-Type": "application/json", "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=130) as resp:
        rpc = json.load(resp)
    res = rpc["result"]
    envelope = json.loads(res["content"][0]["text"])
    return res.get("isError", False), envelope


for engine, q in CASES:
    try:
        is_err, env = call(engine, q)
        data = env.get("data") or {}
        if not isinstance(data, dict):
            data = {}
        counts = []
        for key in ("organic", "images", "videos", "news", "shopping", "local", "maps", "visual_matches", "results", "organic_results", "patents", "scholar"):
            v = data.get(key)
            if isinstance(v, list):
                counts.append(f"{key}={len(v)}")
        first = ""
        for key in ("organic", "images", "videos", "news", "shopping", "local", "visual_matches", "results", "organic_results", "patents"):
            v = data.get(key)
            if isinstance(v, list) and v:
                item = v[0]
                title = item.get("title") or item.get("name") or item.get("source") or ""
                link = item.get("link") or item.get("thumbnail") or item.get("url") or ""
                first = f"first[{key}]: {str(title)[:60]} | {str(link)[:70]}"
                break
        print(f"[{engine}] isError={is_err} ok={env.get('ok')} status={env.get('status')} "
              f"code={data.get('code')} credits={data.get('credits')} {' '.join(counts)}")
        if first:
            print(f"    {first}")
        if is_err or not env.get("ok"):
            text = json.dumps(env, ensure_ascii=False)
            print(f"    DETAIL: {text[:400]}")
    except Exception as exc:
        print(f"[{engine}] EXCEPTION: {exc}")
