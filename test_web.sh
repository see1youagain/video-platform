#!/bin/bash

echo "=== 视频平台 Web 测试 ==="

# 检查文件
echo "检查文件结构..."
files=(
    "web/templates/index.html"
    "web/templates/login.html"
    "web/templates/upload.html"
    "web/templates/files.html"
    "web/templates/play.html"
    "web/static/css/style.css"
    "web/static/js/common.js"
    "web/static/js/upload.js"
)

for f in "${files[@]}"; do
    if [ -f "$f" ]; then
        echo "  ✓ $f"
    else
        echo "  ✗ $f (缺失)"
    fi
done

# 检查服务是否运行
echo ""
echo "检查服务状态..."
if curl -s http://localhost:8080/health > /dev/null 2>&1; then
    echo "  ✓ 服务运行中"
    
    echo ""
    echo "测试页面响应..."
    for page in "/" "/login" "/upload" "/files"; do
        code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:8080$page")
        if [ "$code" = "200" ]; then
            echo "  ✓ $page -> $code"
        else
            echo "  ✗ $page -> $code"
        fi
    done
    
    echo ""
    echo "测试 API..."
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        -d '{"username":"testuser","password":"testpass123"}' \
        "http://localhost:8080/api/v1/auth/register")
    echo "  注册 API -> $code (200 或 400 都正常)"
    
else
    echo "  ✗ 服务未运行，请先启动: go run cmd/server/main.go"
fi

echo ""
echo "=== 测试完成 ==="
echo "在浏览器中打开: http://localhost:8080"
