// 页面加载时更新导航栏
document.addEventListener('DOMContentLoaded', function () {
    updateNavUser();
});

function updateNavUser() {
    const navUser = document.getElementById('nav-user');
    if (!navUser) return;

    if (API.isLoggedIn()) {
        navUser.innerHTML = `
            <span class="username">已登录</span>
            <button class="btn btn-secondary" onclick="API.logout()">退出</button>
        `;
    } else {
        navUser.innerHTML = `
            <a href="/login" class="btn btn-secondary">登录</a>
            <a href="/register" class="btn btn-primary">注册</a>
        `;
    }
}
