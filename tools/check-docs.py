"""Check local documentation links without network access or extra packages."""

from pathlib import Path
import re
from urllib.parse import unquote, urlsplit


def markdown_text(path):
    return re.sub(r"```.*?```", "", path.read_text(), flags=re.S)


def anchors(path):
    result = set()
    for title in re.findall(r"^#{1,6}\s+(.+?)\s*#*$", markdown_text(path), re.M):
        base = re.sub(r"[^\w\- ]", "", title.lower()).replace(" ", "-")
        slug, suffix = base, 0
        while slug in result:
            suffix += 1
            slug = f"{base}-{suffix}"
        result.add(slug)
    return result


def main():
    root = Path(__file__).resolve().parents[1]
    documents = [root / "README.md", *sorted((root / "docs").rglob("*.md"))]
    failures = []
    checked = 0
    for document in documents:
        # Ignore fenced examples; only actual Markdown links are navigation.
        text = markdown_text(document)
        for match in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", text):
            target = match.group(1).strip().strip("<>")
            url = urlsplit(target)
            if url.scheme or url.netloc:
                continue
            path = (document.parent / unquote(url.path)).resolve() if url.path else document
            checked += 1
            if not path.is_relative_to(root) or not path.exists():
                failures.append(f"{document.relative_to(root)}: missing local target {target}")
            elif url.fragment and path.suffix == ".md" and unquote(url.fragment) not in anchors(path):
                failures.append(f"{document.relative_to(root)}: missing heading {target}")
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"Documentation: {len(documents)} files, {checked} local links checked")


if __name__ == "__main__":
    main()
