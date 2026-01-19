const API_BASE = '/api/v1';

const API = {
    token: localStorage.getItem('token'),

    setToken(token) {
        this.token = token;
        if (token) {
            localStorage.setItem('token', token);
        } else {
            localStorage.removeItem('token');
        }
    },

    getToken() {
        return this.token || localStorage.getItem('token');
    },

    isLoggedIn() {
        return !!this.getToken();
    },

    async request(method, path, body = null) {
        const headers = {
            'Content-Type': 'application/json',
        };

        const token = this.getToken();
        if (token) {
            headers['Authorization'] = `Bearer ${token}`;
        }

        const options = { method, headers };
        if (body) {
            options.body = JSON.stringify(body);
        }

        const resp = await fetch(API_BASE + path, options);
        const data = await resp.json();

        if (!resp.ok) {
            throw new Error(data.error || '请求失败');
        }

        return data;
    },

    async register(username, password) {
        return this.request('POST', '/auth/register', { username, password });
    },

    async login(username, password) {
        const data = await this.request('POST', '/auth/login', { username, password });
        this.setToken(data.token);
        return data;
    },

    logout() {
        this.setToken(null);
        window.location.href = '/login';
    },

    async getFiles() {
        const data = await this.request('GET', '/files');
        return data.files || [];
    },

    async getFile(hash) {
        return this.request('GET', `/files/${hash}`);
    },

    async deleteFile(hash) {
        return this.request('DELETE', `/files/${hash}`);
    },

    async initUpload(fileHash, fileName, fileSize) {
        return this.request('POST', '/upload/init', {
            file_hash: fileHash,
            file_name: fileName,
            file_size: fileSize
        });
    },

    async uploadChunk(formData) {
        const token = this.getToken();
        const resp = await fetch(API_BASE + '/upload/chunk', {
            method: 'POST',
            headers: token ? { 'Authorization': `Bearer ${token}` } : {},
            body: formData
        });

        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || '上传分片失败');
        }
        return data;
    },

    async mergeChunks(params) {
        return this.request('POST', '/upload/merge', params);
    }
};

// 工具函数
function formatSize(bytes) {
    if (bytes < 1024) return bytes + 'B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
    return (bytes / (1024 * 1024 * 1024)).toFixed(1) + 'GB';
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
