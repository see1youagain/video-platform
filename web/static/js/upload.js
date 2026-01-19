// 上传功能

const CHUNK_SIZE = 5 * 1024 * 1024; // 5MB
let selectedFile = null;
let fileHash = null;
let contentID = null;
let uploadedChunks = [];

document.addEventListener('DOMContentLoaded', function () {
    requireAuth();

    const fileInput = document.getElementById('fileInput');
    const uploadZone = document.getElementById('uploadZone');

    fileInput.addEventListener('change', handleFileSelect);

    uploadZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        uploadZone.classList.add('dragover');
    });

    uploadZone.addEventListener('dragleave', () => {
        uploadZone.classList.remove('dragover');
    });

    uploadZone.addEventListener('drop', (e) => {
        e.preventDefault();
        uploadZone.classList.remove('dragover');
        if (e.dataTransfer.files.length > 0) {
            fileInput.files = e.dataTransfer.files;
            handleFileSelect();
        }
    });
});

async function handleFileSelect() {
    const fileInput = document.getElementById('fileInput');
    if (!fileInput.files.length) return;

    selectedFile = fileInput.files[0];

    document.getElementById('fileInfo').style.display = 'block';
    document.getElementById('fileName').textContent = selectedFile.name;
    document.getElementById('fileSize').textContent = formatSize(selectedFile.size);
    document.getElementById('fileMD5').textContent = '计算中...';
    document.getElementById('uploadBtn').style.display = 'none';

    // 计算 MD5
    try {
        fileHash = await calculateMD5(selectedFile);
        document.getElementById('fileMD5').textContent = fileHash;
        document.getElementById('uploadBtn').style.display = 'block';
    } catch (err) {
        document.getElementById('fileMD5').textContent = '计算失败: ' + err.message;
    }
}

function calculateMD5(file) {
    return new Promise((resolve, reject) => {
        const chunkSize = 2 * 1024 * 1024;
        const chunks = Math.ceil(file.size / chunkSize);
        const spark = new SparkMD5.ArrayBuffer();
        const reader = new FileReader();
        let currentChunk = 0;

        reader.onload = function (e) {
            spark.append(e.target.result);
            currentChunk++;

            if (currentChunk < chunks) {
                loadNext();
            } else {
                resolve(spark.end());
            }
        };

        reader.onerror = function () {
            reject(new Error('读取文件失败'));
        };

        function loadNext() {
            const start = currentChunk * chunkSize;
            const end = Math.min(start + chunkSize, file.size);
            reader.readAsArrayBuffer(file.slice(start, end));
        }

        loadNext();
    });
}

async function startUpload() {
    if (!selectedFile || !fileHash) return;

    const progressContainer = document.getElementById('progressContainer');
    const progressFill = document.getElementById('progressFill');
    const progressText = document.getElementById('progressText');
    const progressPercent = document.getElementById('progressPercent');
    const uploadBtn = document.getElementById('uploadBtn');
    const resultDiv = document.getElementById('uploadResult');

    progressContainer.style.display = 'block';
    uploadBtn.disabled = true;
    resultDiv.style.display = 'none';

    try {
        // 1. 初始化上传
        progressText.textContent = '初始化上传...';
        const initResp = await authFetch('/api/v1/upload/init', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                file_name: selectedFile.name,
                file_hash: fileHash,
                file_size: selectedFile.size
            })
        });

        const initData = await initResp.json();
        if (!initResp.ok) throw new Error(initData.error || '初始化失败');

        contentID = initData.content_id;
        uploadedChunks = initData.uploaded_chunks || [];

        // 秒传
        if (initData.status === 'fast_upload') {
            showResult(true, '秒传成功！');
            return;
        }

        // 2. 上传分片
        const totalChunks = Math.ceil(selectedFile.size / CHUNK_SIZE);
        const uploadedSet = new Set(uploadedChunks);
        let completed = uploadedChunks.length;

        if (initData.status === 'resumable') {
            progressText.textContent = `断点续传: 已上传 ${completed}/${totalChunks} 分片`;
        }

        const concurrency = 3;
        const queue = [];

        for (let i = 0; i < totalChunks; i++) {
            if (uploadedSet.has(i)) continue;
            queue.push(i);
        }

        const uploadChunk = async (index) => {
            const start = index * CHUNK_SIZE;
            const end = Math.min(start + CHUNK_SIZE, selectedFile.size);
            const chunk = selectedFile.slice(start, end);

            const formData = new FormData();
            formData.append('content_id', contentID);
            formData.append('file_hash', fileHash);
            formData.append('chunk_index', index);
            formData.append('total_chunks', totalChunks);
            formData.append('chunk', chunk, `chunk_${index}`);

            const resp = await authFetch('/api/v1/upload/chunk', {
                method: 'POST',
                body: formData
            });

            if (!resp.ok) {
                const data = await resp.json();
                throw new Error(data.error || `分片 ${index} 上传失败`);
            }

            completed++;
            const percent = Math.round((completed / totalChunks) * 100);
            progressFill.style.width = percent + '%';
            progressPercent.textContent = percent + '%';
            progressText.textContent = `上传中: ${completed}/${totalChunks} 分片`;
        };

        // 并发上传
        const workers = [];
        for (let i = 0; i < concurrency; i++) {
            workers.push((async () => {
                while (queue.length > 0) {
                    const index = queue.shift();
                    if (index !== undefined) {
                        await uploadChunk(index);
                    }
                }
            })());
        }

        await Promise.all(workers);

        // 3. 合并分片
        progressText.textContent = '合并文件中...';
        const mergeResp = await authFetch('/api/v1/upload/merge', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                content_id: contentID,
                file_hash: fileHash,
                file_name: selectedFile.name,
                total_chunks: totalChunks,
                file_size: selectedFile.size
            })
        });

        const mergeData = await mergeResp.json();
        if (!mergeResp.ok) throw new Error(mergeData.error || '合并失败');

        showResult(true, '上传成功！');

    } catch (err) {
        showResult(false, '❌ ' + err.message);
    } finally {
        uploadBtn.disabled = false;
    }
}

function showResult(success, message) {
    const resultDiv = document.getElementById('uploadResult');
    resultDiv.style.display = 'block';
    resultDiv.className = 'upload-result ' + (success ? 'success' : 'error');
    resultDiv.innerHTML = message + (success ? '<br><a href="/files">查看我的文件</a>' : '');
}
