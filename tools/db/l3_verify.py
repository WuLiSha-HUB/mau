import json, os

LIB = r"C:\Users\ASUS\AppData\Local\CatHome4\Data\db\l3historytest\entries.jsonl"
SRC = r"C:\Users\ASUS\Desktop\MauTest\cat-cat-big-party\L3\history"

index = {}
for dirpath, dirs, files in os.walk(SRC):
    for f in files:
        if f.endswith(".md"):
            rel = os.path.relpath(os.path.join(dirpath, f), SRC)
            index[(rel.split("\\")[0] if "\\" in rel else "(root)", f[:-3])] = os.path.join(dirpath, f)

# 跨目录同名 / 标题带目录后缀的显式映射
ALIAS = {
    "project_TT": ("TT", "project"),
    "2026-08-27_Mau版本未同步_R2.2识图基座": ("Mau", "2026-08-27_Mau版本未同步_R2.2识图基座"),
    "2026-08-27_Mau版本未同步_R2.2识图基座_Git": ("Git", "2026-08-27_Mau版本未同步_R2.2识图基座"),
}

rows = {}
with open(LIB, encoding="utf-8") as fh:
    for line in fh:
        line = line.strip()
        if not line:
            continue
        try:
            rec = json.loads(line)
        except Exception:
            continue
        if "_" in rec:
            continue
        cid = rec.get("id")
        if cid not in rows or rec.get("v", 0) > rows[cid].get("v", 0):
            rows[cid] = rec

same = 0
for cid in sorted(rows):
    rec = rows[cid]
    title = rec["title"]
    if title in ALIAS:
        src = index.get(ALIAS[title])
    else:
        src = None
        for (d, t), p in index.items():
            if t == title:
                src = p
                break
    if not src:
        print("SRC_MISSING", cid, title)
        continue
    with open(src, encoding="utf-8") as f:
        text = f.read()
    a = rec["body"].replace("\r\n", "\n").strip()
    b = text.lstrip("\ufeff").replace("\r\n", "\n").strip()
    ok = a == b
    if not ok:
        n = min(len(a), len(b))
        where = "len_only"
        for i in range(n):
            if a[i] != b[i]:
                where = "at %d" % i
                break
        print("DIFF", cid, title, "db", len(a), "src", len(b), where, src.replace(SRC, ""))
    same += 1 if ok else 0
print("TOTAL", len(rows), "SAME", same)
