#!/usr/bin/env bash
set -e

# 获取最新 tag
LATEST_TAG=$(git tag --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
if [ -z "$LATEST_TAG" ]; then
  LATEST_TAG="v0.0.0"
fi

echo "当前最新 tag: $LATEST_TAG"

# 解析版本号
IFS='.' read -r MAJOR MINOR PATCH <<< "${LATEST_TAG#v}"

# 如果传入了版本号则使用，否则默认 patch+1
if [ -n "$1" ]; then
  NEW_TAG="$1"
  # 补全 v 前缀
  [[ "$NEW_TAG" != v* ]] && NEW_TAG="v$NEW_TAG"
else
  NEW_TAG="v${MAJOR}.${MINOR}.$((PATCH + 1))"
fi

echo "即将发布: $NEW_TAG"
read -p "确认? (y/N) " CONFIRM
if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
  echo "已取消"
  exit 0
fi

# 检查工作区是否干净
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "工作区有未提交的改动，请先 commit 或 stash"
  exit 1
fi

# go mod tidy
echo "→ go mod tidy"
go mod tidy

# 如果 go mod tidy 产生了变更，自动提交
if ! git diff --quiet go.mod go.sum 2>/dev/null; then
  git add go.mod go.sum
  git commit -m "chore: go mod tidy before $NEW_TAG"
fi

# 打 tag 并推送
echo "→ git tag $NEW_TAG"
git tag "$NEW_TAG"

echo "→ git push origin main $NEW_TAG"
git push origin main "$NEW_TAG"

echo "✓ 发布成功: github.com/crosszan/modu@$NEW_TAG"
