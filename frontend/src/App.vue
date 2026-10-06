<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import {
  CircleCheck,
  CircleClose,
  Document as DocumentIcon,
  FolderOpened,
  Lock,
  Refresh,
  VideoPause,
  VideoPlay,
  Warning,
} from '@element-plus/icons-vue'
import { Events } from '@wailsio/runtime'
import { Service } from '../bindings/zipcrypt/internal/archive/index.js'
import type {
  Bootstrap,
  LogLine,
  Progress,
  Result,
  SevenZipInfo,
  SourceInfo,
} from '../bindings/zipcrypt/internal/archive/index.js'

// ElMessage / ElMessageBox 由 unplugin-auto-import 自动注册，无需显式 import

// ---------------------------------------------------------------- 状态

const sourceDir = ref('')
const outputDir = ref('')
const archiveName = ref('')
// 密码是后端硬编码常量（internal/archive 的 ArchivePassword），这里只读展示，不可修改
const fixedPassword = ref('')
const splitSubDirs = ref(false)
const deleteSource = ref(false)
const overwrite = ref(true)

const info = ref<SourceInfo | null>(null)
const sevenZip = ref<SevenZipInfo | null>(null)
const scanning = ref(false)
const running = ref(false)
const jobId = ref('')
const progress = ref<Progress | null>(null)
const logs = ref<string[]>([])
const result = ref<Result | null>(null)

let offEvents: Array<() => void> = []
let scanTimer: number | undefined

// ---------------------------------------------------------------- 派生

/** 预计产出：拆分模式按子目录算，整体模式只有一个包 */
const plannedArchives = computed(() => {
  if (!info.value) return []
  if (splitSubDirs.value) {
    const list = (info.value.subDirs ?? []).map((d) => ({
      name: `${archiveName.value ? archiveName.value + '-' : ''}${d.name}.7z`,
      files: d.fileCount,
      size: d.totalSize,
    }))
    if (info.value.looseFiles > 0) {
      list.push({
        name: `${archiveName.value ? archiveName.value + '-' : ''}根目录文件.7z`,
        files: info.value.looseFiles,
        size: info.value.looseSize,
      })
    }
    return list
  }
  return [
    {
      name: `${archiveName.value || info.value.name}.7z`,
      files: info.value.fileCount,
      size: info.value.totalSize,
    },
  ]
})

const plannedTotal = computed(() => plannedArchives.value.reduce((s, a) => s + a.size, 0))

/** -mx0 只做存储，速度基本等于磁盘读速，150MB/s 是个保守估计 */
const estimate = computed(() => {
  const sec = plannedTotal.value / (150 * 1024 * 1024)
  if (sec < 1) return '不到 1 秒'
  if (sec < 60) return `约 ${Math.ceil(sec)} 秒`
  return `约 ${Math.ceil(sec / 60)} 分钟`
})

const percent = computed(() => {
  const p = progress.value?.percent ?? 0
  return Math.max(0, Math.min(100, Math.round(p * 10) / 10))
})

const sevenZipOk = computed(() => sevenZip.value?.found === true)

/** 展示给用户核对的命令（密码已打码） */
const commandPreview = computed(() => {
  if (!progress.value?.command) {
    const name = plannedArchives.value[0]?.name ?? 'xxx.7z'
    return `7z a -mx0 -p*** -mhe=on ${name} .\\${info.value?.name ?? '目录'}`
  }
  return progress.value.command
})

const statusText = computed(() => {
  if (!running.value && !result.value) return '待命'
  return progress.value?.message || (result.value ? result.value.error : '待命')
})

const canStart = computed(
  () => !running.value && !!info.value && plannedArchives.value.length > 0,
)

const archiveStatus = computed(() => {
  if (!result.value) return ''
  if (result.value.success) return 'success'
  if (result.value.cancelled) return 'info'
  return 'error'
})

// ---------------------------------------------------------------- 工具

function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(2)} ${units[i]}`
}

function fmtSpeed(n: number | undefined): string {
  if (!n || n <= 0) return ''
  return `${fmtBytes(n)}/s`
}

function fmtDuration(ms: number | undefined): string {
  if (!ms || ms < 0) return ''
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)} 秒`
  const m = Math.floor(s / 60)
  return `${m} 分 ${Math.round(s % 60)} 秒`
}

function pushLog(text: string) {
  logs.value.push(text)
  if (logs.value.length > 400) logs.value.splice(0, logs.value.length - 400)
  // 日志区始终贴底
  requestAnimationFrame(() => {
    const el = document.getElementById('log-box')
    if (el) el.scrollTop = el.scrollHeight
  })
}

// ---------------------------------------------------------------- 交互

async function loadSource(path: string, keepOutput = false) {
  sourceDir.value = path
  if (!path) {
    info.value = null
    return
  }
  scanning.value = true
  try {
    const data = await Service.Inspect(path)
    info.value = data
    // 没手动指定过输出目录时，默认落在源目录的上一级，
    // 避免压缩包被打进自己所在的源文件夹里
    if (!keepOutput || !outputDir.value) outputDir.value = data.defaultOut
    if (!archiveName.value) archiveName.value = data.defaultName
    result.value = null
    progress.value = null
  } catch (e) {
    info.value = null
    ElMessage.error(`无法读取该文件夹：${(e as Error)?.message ?? e}`)
  } finally {
    scanning.value = false
  }
}

async function chooseSource() {
  try {
    const p = await Service.PickSourceDir(sourceDir.value)
    if (p) await loadSource(p, false)
  } catch (e) {
    if ((e as Error)?.message) ElMessage.error(String((e as Error).message))
  }
}

async function chooseOutput() {
  try {
    const p = await Service.PickOutputDir(outputDir.value)
    if (p) outputDir.value = p
  } catch (e) {
    if ((e as Error)?.message) ElMessage.error(String((e as Error).message))
  }
}

/** 手动改路径时防抖重新扫描 */
watch(sourceDir, (val) => {
  if (!val || val === info.value?.path) return
  window.clearTimeout(scanTimer)
  scanTimer = window.setTimeout(() => {
    if (val !== info.value?.path) void loadSource(val, true)
  }, 600)
})

async function start() {
  if (!canStart.value || !info.value) return

  if (deleteSource.value) {
    const n = splitSubDirs.value ? (info.value.subDirs?.length ?? 0) : 1
    try {
      await ElMessageBox.confirm(
        `加密成功后将从磁盘删除源文件：${n} 个文件夹，共 ${fmtBytes(plannedTotal.value)}。` +
          '删除不可撤销（会写一份删除清单日志到 %APPDATA%\\ZipCrypt 便于事后核对）。确认继续？',
        '确认删除源文件？',
        { type: 'warning', confirmButtonText: '确认删除并加密', cancelButtonText: '再想想' },
      )
    } catch {
      return
    }
  }

  logs.value = []
  progress.value = null
  result.value = null

  try {
    await Service.SaveSettings({
      sourceDir: sourceDir.value,
      outputDir: outputDir.value,
      archiveName: archiveName.value,
      sevenZipPath: sevenZip.value?.path ?? '',
      splitSubDirs: splitSubDirs.value,
      deleteSource: deleteSource.value,
      overwrite: overwrite.value,
    })
  } catch {
    // 设置保存失败不阻塞加密
  }

  try {
    jobId.value = await Service.Start({
      sourceDir: sourceDir.value,
      outputDir: outputDir.value,
      archiveName: archiveName.value,
      splitSubDirs: splitSubDirs.value,
      deleteSource: deleteSource.value,
      overwrite: overwrite.value,
    })
    running.value = true
  } catch (e) {
    running.value = false
    ElMessage.error(String((e as Error)?.message ?? e))
  }
}

async function cancel() {
  if (!jobId.value) return
  try {
    await Service.Cancel(jobId.value)
    ElMessage.info('已发出取消指令，正在收尾……')
  } catch (e) {
    ElMessage.error(String((e as Error)?.message ?? e))
  }
}

async function openOutput() {
  if (!result.value?.outputDir) return
  try {
    await Service.OpenOutputDir(result.value.outputDir, result.value.archives?.[0] ?? '')
  } catch (e) {
    ElMessage.error(String((e as Error)?.message ?? e))
  }
}

async function openFolderOnly() {
  if (!result.value?.outputDir) return
  try {
    await Service.OpenOutputDir(result.value.outputDir, '')
  } catch (e) {
    ElMessage.error(String((e as Error)?.message ?? e))
  }
}

async function reveal(file: string) {
  const dir = file.replace(/[\\/][^\\/]+$/, '')
  try {
    await Service.OpenOutputDir(dir, file)
  } catch (e) {
    ElMessage.error(String((e as Error)?.message ?? e))
  }
}

async function redetect() {
  sevenZip.value = await Service.DetectSevenZip()
  ElMessage[sevenZip.value.found ? 'success' : 'warning'](
    sevenZip.value.found ? `就绪：${sevenZip.value.path}` : sevenZip.value.message,
  )
}

// ---------------------------------------------------------------- 事件

onMounted(async () => {
  offEvents = [
    Events.On('archive:progress', (ev) => {
      progress.value = ev.data as Progress
    }),
    Events.On('archive:log', (ev) => {
      const l = ev.data as LogLine
      pushLog(l.text)
    }),
    Events.On('archive:done', (ev) => {
      const r = ev.data as Result
      result.value = r
      running.value = false
      if (r.success) {
        ElMessage.success(r.error)
      } else if (r.cancelled) {
        ElMessage.info(r.error)
      } else {
        ElMessage.error(r.error)
      }
      // 加密完把输出目录的压缩包列出来，方便直接点开
      if (info.value) void loadSource(info.value.path, true)
    }),
  ]

  try {
    const boot = (await Service.Bootstrap()) as Bootstrap
    sevenZip.value = boot.sevenZip
    if (boot.settings?.sourceDir) {
      await loadSource(boot.settings.sourceDir, true)
      outputDir.value = boot.settings.outputDir || outputDir.value
      archiveName.value = boot.settings.archiveName || archiveName.value
    }
    splitSubDirs.value = !!boot.settings?.splitSubDirs
    deleteSource.value = !!boot.settings?.deleteSource
    overwrite.value = boot.settings?.overwrite ?? true
    fixedPassword.value = boot.password ?? ''
  } catch (e) {
    ElMessage.error(`初始化失败：${String((e as Error)?.message ?? e)}`)
  }
})

onBeforeUnmount(() => {
  offEvents.forEach((off) => off())
  window.clearTimeout(scanTimer)
})
</script>

<template>
  <el-config-provider :locale="zhCn">
    <div class="page">
      <!-- 顶栏 -->
      <header class="topbar">
        <div class="brand">
          <el-icon :size="22"><Lock /></el-icon>
          <div>
            <h1>ZipCrypt · 快速归档加密</h1>
            <p class="sub">7z a -mx0 -p"******" -mhe=on &nbsp;·&nbsp; 文件头一并加密，不压缩只打包，最快</p>
          </div>
        </div>
        <div class="env">
          <el-tag v-if="sevenZipOk" type="success" effect="light" size="small">
            {{ sevenZip?.version || '7-Zip' }} 就绪
          </el-tag>
          <el-tag v-else type="danger" effect="light" size="small">未找到 7z.exe</el-tag>
          <el-tooltip v-if="sevenZip" :content="sevenZip.path || sevenZip.message" placement="bottom">
            <span class="env-path">{{ sevenZip.path || sevenZip.message }}</span>
          </el-tooltip>
          <el-button size="small" :icon="Refresh" text @click="redetect">重新检测</el-button>
        </div>
      </header>

      <el-alert
        v-if="sevenZip && !sevenZip.found"
        type="error"
        show-icon
        :closable="false"
        title="没有找到 7z.exe"
        description="请先安装 7-Zip（https://www.7-zip.org），然后点右上角“重新检测”。"
        class="block"
      />

      <!-- 源文件夹 -->
      <el-card shadow="never" class="block">
        <template #header>
          <div class="card-head">
            <span class="step">1</span>
            <span>源文件夹</span>
          </div>
        </template>
        <div class="row">
          <el-input
            v-model="sourceDir"
            placeholder="选择要加密的文件夹，或直接粘贴路径"
            clearable
            :disabled="running"
          />
          <el-button :icon="FolderOpened" :disabled="running" @click="chooseSource">选择文件夹</el-button>
        </div>
        <div v-if="scanning" class="hint">正在统计…</div>
        <div v-else-if="info" class="stats">
          <el-tag size="small" type="info">{{ info.fileCount }} 个文件</el-tag>
          <el-tag size="small" type="info">{{ info.dirCount }} 个目录</el-tag>
          <el-tag size="small" type="info">{{ fmtBytes(info.totalSize) }}</el-tag>
          <el-tag v-if="info.skipped > 0" size="small" type="warning">{{ info.skipped }} 项无法读取</el-tag>
          <span class="hint">扫描耗时 {{ info.scanDuration }} ms</span>
        </div>
      </el-card>

      <!-- 输出设置 -->
      <el-card shadow="never" class="block">
        <template #header>
          <div class="card-head">
            <span class="step">2</span>
            <span>压缩包</span>
          </div>
        </template>
        <el-form label-width="88px" label-position="left">
          <el-form-item label="输出目录">
            <div class="row">
              <el-input v-model="outputDir" :disabled="running" placeholder="默认是源文件夹的上一级" />
              <el-button :icon="FolderOpened" :disabled="running" @click="chooseOutput">选择</el-button>
            </div>
          </el-form-item>
          <el-form-item label="文件名">
            <el-input v-model="archiveName" :disabled="running" placeholder="留空则用文件夹名">
              <template #append>.7z</template>
            </el-input>
            <div v-if="splitSubDirs" class="hint">
              拆分模式下这里是<strong>前缀</strong>：每个子文件夹生成「前缀-子目录名.7z」，留空则只用子目录名。
            </div>
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="fixedPassword" disabled>
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
            <div class="hint">
              密码已硬编码在程序里（<code>internal/archive/settings.go</code> 的
              <code>ArchivePassword</code>），界面上不可修改，也不写入配置文件。
            </div>
          </el-form-item>
          <el-form-item label="">
            <el-checkbox v-model="overwrite" :disabled="running">覆盖同名压缩包（7z -y）</el-checkbox>
          </el-form-item>
        </el-form>
      </el-card>

      <!-- 选项 -->
      <el-card shadow="never" class="block">
        <template #header>
          <div class="card-head">
            <span class="step">3</span>
            <span>选项</span>
          </div>
        </template>

        <div class="switch-row" :class="{ dim: running }">
          <div>
            <el-switch v-model="splitSubDirs" :disabled="running" />
            <span class="switch-label">归档子目录到单独文件</span>
          </div>
          <p class="hint">
            关闭：把所选文件夹整体打成一个加密包（包内保留原文件夹名）。<br />
            开启：不加密所选文件夹本身，而是把它下面的<strong>每个子文件夹各自打成一个独立加密包</strong>
            （根目录散落文件合成一个包）。
          </p>
        </div>

        <el-divider />

        <div class="switch-row" :class="{ dim: running }">
          <div>
            <el-switch v-model="deleteSource" :disabled="running" />
            <span class="switch-label danger">加密成功后删除源文件</span>
          </div>
          <p class="hint">
            默认关闭。开启后会先弹确认框；删除清单会写到 %APPDATA%\ZipCrypt\removed-*.log 便于事后核对。
          </p>
        </div>

        <template v-if="plannedArchives.length > 0">
          <el-divider />
          <div class="hint" style="margin-bottom: 8px">
            将生成 <strong>{{ plannedArchives.length }}</strong> 个压缩包，合计约
            {{ fmtBytes(plannedTotal) }}，预计 {{ estimate }}：
          </div>
          <div class="plan-list">
            <div v-for="a in plannedArchives.slice(0, 8)" :key="a.name" class="plan-item">
              <el-icon><DocumentIcon /></el-icon>
              <span class="plan-name">{{ a.name }}</span>
              <span class="hint">{{ a.files }} 个文件 · {{ fmtBytes(a.size) }}</span>
            </div>
            <div v-if="plannedArchives.length > 8" class="hint">
              …以及另外 {{ plannedArchives.length - 8 }} 个
            </div>
          </div>
        </template>
      </el-card>

      <!-- 命令预览 + 操作 -->
      <el-card shadow="never" class="block">
        <div class="cmd" :title="commandPreview">{{ commandPreview }}</div>
        <div class="actions">
          <el-button
            type="primary"
            size="large"
            :icon="VideoPlay"
            :disabled="!canStart"
            :loading="running"
            @click="start"
          >
            开始加密
          </el-button>
          <el-button size="large" :icon="VideoPause" :disabled="!running" @click="cancel">取消</el-button>
          <div class="spacer" />
          <el-button
            size="large"
            :icon="FolderOpened"
            :disabled="!outputDir"
            @click="openFolderOnly"
          >
            打开输出目录
          </el-button>
        </div>
      </el-card>

      <!-- 进度 -->
      <el-card v-if="running || result" shadow="never" class="block">
        <template #header>
          <div class="card-head">
            <el-icon v-if="running" class="spin"><Refresh /></el-icon>
            <el-icon v-else-if="result?.success" color="#67c23a"><CircleCheck /></el-icon>
            <el-icon v-else-if="result?.cancelled" color="#909399"><CircleClose /></el-icon>
            <el-icon v-else color="#f56c6c"><Warning /></el-icon>
            <span>{{ statusText }}</span>
          </div>
        </template>

        <el-progress
          :percentage="percent"
          :status="result ? (archiveStatus as any) : undefined"
          :stroke-width="14"
          :duration="6"
        />

        <div v-if="progress" class="stats progress-stats">
          <el-tag v-if="progress.steps > 1" size="small" type="info">
            [{{ progress.step }}/{{ progress.steps }}] {{ progress.archive }}
          </el-tag>
          <span v-if="progress.written > 0" class="hint">
            已写 {{ fmtBytes(progress.written) }} / {{ fmtBytes(progress.total) }}
          </span>
          <span v-if="progress.speed > 0" class="hint">{{ fmtSpeed(progress.speed) }}</span>
          <span v-if="progress.file" class="hint file">{{ progress.file }}</span>
        </div>

        <div v-if="result" class="result-block">
          <el-alert
            :type="(archiveStatus as any)"
            :title="result.error"
            :closable="false"
            show-icon
            class="block"
          />
          <div v-if="result.archives?.length" class="plan-list">
            <div v-for="a in result.archives" :key="a" class="plan-item">
              <el-icon><DocumentIcon /></el-icon>
              <span class="plan-name" :title="a">{{ a }}</span>
              <el-button size="small" text type="primary" @click="reveal(a)">打开所在目录</el-button>
            </div>
          </div>
          <div v-if="result.failed?.length" class="fail-list">
            <div v-for="f in result.failed" :key="f" class="fail-item">{{ f }}</div>
          </div>
          <div v-if="result.removedLog" class="hint">
            删除清单：{{ result.removedLog }}
          </div>
          <div class="actions">
            <el-button type="primary" :icon="FolderOpened" @click="openOutput">打开加密文件所在目录</el-button>
            <el-button :icon="FolderOpened" @click="openFolderOnly">只打开目录</el-button>
            <span v-if="result.elapsedMs > 0" class="hint">用时 {{ fmtDuration(result.elapsedMs) }}</span>
          </div>
        </div>
      </el-card>

      <!-- 日志 -->
      <el-card shadow="never" class="block">
        <template #header>
          <div class="card-head">
            <span>7z 输出</span>
            <el-button v-if="logs.length" size="small" text @click="logs = []">清空</el-button>
          </div>
        </template>
        <div id="log-box" class="log-box">
          <div v-if="!logs.length" class="hint">暂无输出</div>
          <div v-for="(l, i) in logs" :key="i" class="log-line">{{ l }}</div>
        </div>
      </el-card>
    </div>
  </el-config-provider>
</template>
