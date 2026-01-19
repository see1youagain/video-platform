// 通用工具函数

function getToken() {
    return localStorage.getItem('token');
}

function getUsername() {
    return localStorage.getItem('username');
}

function isLoggedIn() {
    return !!getToken();
}

function logout() {
    localStorage.removeItem('token');
    localStorage.removeItem('username');
    window.location.href = '/login';
}

function requireAuth() {
    if (!isLoggedIn()) {
        window.location.href = '/login';
        return false;
    }
    return true;
}

function checkAuth() {
    const navLinks = document.getElementById('navLinks');
    const navUser = document.getElementById('navUser');
    const usernameEl = document.getElementById('username');

    if (isLoggedIn()) {
        if (navLinks) navLinks.style.display = 'none';
        if (navUser) navUser.style.display = 'block';
        if (usernameEl) usernameEl.textContent = getUsername();
    }
}

async function authFetch(url, options = {}) {
    const token = getToken();
    if (!token) {
        window.location.href = '/login';
        throw new Error('未登录');
    }

    options.headers = options.headers || {};
    options.headers['Authorization'] = 'Bearer ' + token;

    const resp = await fetch(url, options);

    if (resp.status === 401) {
        logout();
        throw new Error('登录已过期');
    }

    return resp;
}

function formatSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
}

// 页面加载时检查登录状态
document.addEventListener('DOMContentLoaded', function () {
    const usernameEl = document.getElementById('username');
    if (usernameEl && isLoggedIn()) {
        usernameEl.textContent = getUsername();
    }
});
