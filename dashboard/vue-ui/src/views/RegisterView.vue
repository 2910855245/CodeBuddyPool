<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { api } from '@/api'

const step = ref<1 | 2 | 3>(1)
const phone = ref('')
const code = ref('')
const busy = ref(false)
const error = ref('')
const sentMsg = ref('')
const result = ref<{ credits: number; bonus: number } | null>(null)

// 60 秒重发倒计时
const countdown = ref(0)
let cdTimer: ReturnType<typeof setInterval> | null = null
function startCountdown() {
  countdown.value = 60
  if (cdTimer) clearInterval(cdTimer)
  cdTimer = setInterval(() => {
    if (countdown.value > 1) countdown.value--
    else { countdown.value = 0; if (cdTimer) { clearInterval(cdTimer); cdTimer = null } }
  }, 1000)
}
onUnmounted(() => { if (cdTimer) clearInterval(cdTimer) })

// 第 1 步点击：先查状态，已在池直接查积分；否则发短信
async function onNext() {
  if (!phone.value.trim()) { error.value = '请输入手机号'; return }
  busy.value = true
  error.value = ''
  try {
    const chk = await api.registerPage.check(phone.value.trim())
    if (chk.success && chk.data?.in_pool) {
      // 已在号池：直接展示积分
      result.value = { credits: chk.data?.credits ?? 0, bonus: 0 }
      step.value = 3
      return
    }
    await sendCode()
  } catch (e: any) {
    error.value = e.message || '操作失败，请稍后重试'
  } finally {
    busy.value = false
  }
}

async function sendCode() {
  const res = await api.registerPage.start(phone.value.trim())
  if (res.success) {
    sentMsg.value = res.data?.message || '验证码已发送'
    step.value = 2
    startCountdown()
  } else {
    error.value = res.message || '发送失败'
  }
}

async function resend() {
  if (countdown.value > 0 || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await sendCode()
  } catch (e: any) {
    error.value = e.message || '发送失败，请稍后重试'
  } finally {
    busy.value = false
  }
}

async function submitCode() {
  if (!code.value.trim()) { error.value = '请输入验证码'; return }
  busy.value = true
  error.value = ''
  try {
    const res = await api.registerPage.submit(phone.value.trim(), code.value.trim())
    if (res.success) {
      result.value = { credits: res.data?.credits ?? 0, bonus: res.data?.bonus ?? 0 }
      step.value = 3
    } else if (res.data?.already_in_pool) {
      // 并发下已入池：拉一次积分展示
      const chk = await api.registerPage.check(phone.value.trim())
      result.value = { credits: chk.data?.credits ?? 0, bonus: 0 }
      step.value = 3
    } else {
      error.value = res.message || '注册失败'
    }
  } catch (e: any) {
    error.value = e.message || '注册失败，请稍后重试'
  } finally {
    busy.value = false
  }
}

function restart() {
  step.value = 1
  phone.value = ''
  code.value = ''
  error.value = ''
  sentMsg.value = ''
  result.value = null
  countdown.value = 0
}
</script>

<template>
  <div class="reg-page">
    <div class="reg-card">
      <div class="reg-logo">
        <svg class="logo-svg" viewBox="0 0 50 50" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
          <defs>
            <linearGradient id="regLogoGrad" x1="0" y1="0" x2="50" y2="50" gradientUnits="userSpaceOnUse">
              <stop stop-color="#22c55e" />
              <stop offset="1" stop-color="#4f6ef7" />
            </linearGradient>
          </defs>
          <rect width="50" height="50" rx="14" fill="url(#regLogoGrad)" />
          <rect x="17" y="12" width="16" height="26" rx="3.5" stroke="#fff" stroke-width="2.5" />
          <path d="M22 33.5h6" stroke="#fff" stroke-width="2.5" stroke-linecap="round" />
          <path d="M35 18v8M31 22h8" stroke="#fff" stroke-width="2.5" stroke-linecap="round" />
        </svg>
      </div>
      <h1 class="reg-title">好友助力注册</h1>
      <p class="reg-sub">填写你的手机号，把收到的短信验证码告诉我就行</p>
      <p class="reg-note">验证码仅用于本次注册，注册完成后此号码会加入朋友的账号共享计划</p>

      <!-- 步骤指示 -->
      <div class="steps">
        <div class="step" :class="{ active: step >= 1, done: step > 1 }">
          <span class="step-num">
            <svg v-if="step > 1" viewBox="0 0 12 12" class="step-check"><path d="M2 6.2l2.6 2.6L10 3.4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            <template v-else>1</template>
          </span><span class="step-label">手机号</span>
        </div>
        <div class="step-line" :class="{ filled: step > 1 }"></div>
        <div class="step" :class="{ active: step >= 2, done: step > 2 }">
          <span class="step-num">
            <svg v-if="step > 2" viewBox="0 0 12 12" class="step-check"><path d="M2 6.2l2.6 2.6L10 3.4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            <template v-else>2</template>
          </span><span class="step-label">验证码</span>
        </div>
        <div class="step-line" :class="{ filled: step > 2 }"></div>
        <div class="step" :class="{ active: step >= 3 }">
          <span class="step-num">3</span><span class="step-label">完成</span>
        </div>
      </div>

      <!-- 步骤 1：手机号 -->
      <div v-if="step === 1" class="step-body">
        <div class="field">
          <label>手机号</label>
          <input
            v-model.trim="phone"
            type="tel"
            placeholder="输入手机号"
            maxlength="13"
            @keyup.enter="onNext"
          />
        </div>
        <div v-if="error" class="err-box">{{ error }}</div>
        <button class="btn btn-primary btn-lg btn-block" :disabled="busy" @click="onNext">
          <span v-if="busy" class="spinner"></span>
          <span>{{ busy ? '查询中...' : '下一步' }}</span>
        </button>
      </div>

      <!-- 步骤 2：验证码 -->
      <div v-else-if="step === 2" class="step-body">
        <div class="sent-tip">{{ sentMsg }}</div>
        <div class="field">
          <label>短信验证码</label>
          <input
            v-model.trim="code"
            type="text"
            placeholder="输入 6 位验证码"
            maxlength="8"
            @keyup.enter="submitCode"
          />
        </div>
        <div v-if="error" class="err-box">{{ error }}</div>
        <button class="btn btn-primary btn-lg btn-block" :disabled="busy" @click="submitCode">
          <span v-if="busy" class="spinner"></span>
          <span>{{ busy ? '注册中...' : '完成注册' }}</span>
        </button>
        <div class="step-links">
          <button class="link-btn" :disabled="busy || countdown > 0" @click="resend">
            {{ countdown > 0 ? `重新发送 (${countdown}s)` : '重新发送验证码' }}
          </button>
          <span class="link-sep">·</span>
          <button class="link-btn" :disabled="busy" @click="step = 1">修改手机号</button>
        </div>
      </div>

      <!-- 步骤 3：完成 -->
      <div v-else class="step-body done-body">
        <div class="done-icon">
          <svg viewBox="0 0 52 52" class="check-svg">
            <circle class="check-circle" cx="26" cy="26" r="24" fill="none" />
            <path class="check-path" fill="none" d="M14 27l8 8 16-16" />
          </svg>
        </div>
        <div class="done-title">注册成功</div>
        <div class="done-credits">
          <div class="credits-num">{{ result?.credits ?? 0 }}</div>
          <div class="credits-label">当前积分</div>
        </div>
        <div v-if="result?.bonus" class="muted">签到奖励 +{{ result.bonus }} 积分</div>
        <div class="muted" style="margin-top: 12px;">已加入朋友的账号共享计划，感谢助力</div>
        <button class="btn btn-outline btn-block" @click="restart">再注册一个</button>
      </div>

      <div class="reg-foot">注册即表示同意加入账号共享计划 · 积分由共享服务统一调度</div>

    </div>
  </div>
</template>


<style scoped>
.reg-page {
  width: 100%;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background:
    radial-gradient(600px 400px at 15% 15%, rgba(52, 199, 89, .09), transparent 70%),
    radial-gradient(560px 420px at 85% 80%, rgba(79, 110, 247, .11), transparent 70%),
    var(--c-bg);
}

.reg-card {
  width: 100%;
  max-width: 420px;
  background: var(--c-surface);
  border: 1px solid var(--c-border);
  border-radius: var(--radius-xl);
  padding: 36px 32px 24px;
  box-shadow: var(--shadow-md);
  animation: fadeInUp .4s var(--ease);
}

.reg-logo { display: flex; justify-content: center; margin-bottom: 14px; }
.logo-svg {
  width: 52px; height: 52px; display: block;
  border-radius: 15px;
  box-shadow: 0 8px 24px rgba(52, 199, 89, .3);
  animation: scaleIn .45s var(--ease);
}

.reg-title { text-align: center; font-size: 21px; font-weight: 600; letter-spacing: -.02em; margin: 0 0 4px; }
.reg-sub { text-align: center; color: var(--c-text-muted); margin: 0 0 8px; font-size: 13.5px; }
.reg-note {
  text-align: center; color: var(--c-text-secondary);
  margin: 0 0 24px; font-size: 12.5px; line-height: 1.6;
  padding: 0 6px;
}

/* 步骤条 */
.steps { display: flex; align-items: center; justify-content: center; margin-bottom: 26px; }
.step { display: flex; flex-direction: column; align-items: center; gap: 5px; }
.step-num {
  width: 28px; height: 28px;
  border-radius: 50%;
  background: rgba(0, 0, 0, .05);
  color: var(--c-text-muted);
  display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 700;
  transition: all .25s var(--ease);
}
.step.active .step-num {
  background: var(--c-primary);
  color: #fff;
  box-shadow: 0 3px 10px rgba(80, 104, 240, .35);
}
.step.done .step-num { background: var(--c-success); box-shadow: 0 3px 10px rgba(52, 199, 89, .3); }
.step-check { width: 13px; height: 13px; }
.step-label { font-size: 11.5px; color: var(--c-text-muted); transition: color .25s var(--ease); }
.step.active .step-label { color: var(--c-text); font-weight: 600; }
.step-line {
  width: 40px; height: 2px;
  background: var(--c-border-strong);
  margin: 0 8px 17px;
  border-radius: 999px;
  transition: background .25s var(--ease);
}
.step-line.filled { background: var(--c-success); }

.step-body { animation: fadeIn .3s var(--ease); }
.sent-tip {
  background: var(--c-success-bg);
  color: var(--c-success);
  border: 1px solid rgba(36, 138, 61, .15);
  border-radius: var(--radius-sm);
  padding: 10px 14px;
  font-size: 13px;
  margin-bottom: 16px;
}
.err-box {
  background: var(--c-danger-bg);
  color: var(--c-danger);
  border: 1px solid rgba(215, 0, 21, .12);
  border-radius: var(--radius-sm);
  padding: 10px 14px;
  font-size: 13px;
  margin-bottom: 14px;
  animation: fadeInUp .25s var(--ease);
}
.step-links {
  display: flex; align-items: center; justify-content: center; gap: 6px;
  margin-top: 14px;
}
.link-btn {
  border: none; background: transparent; cursor: pointer;
  color: var(--c-text-secondary); font-size: 13px; padding: 2px 4px;
  font-family: var(--font);
  transition: color var(--dur) var(--ease);
}
.link-btn:hover:not(:disabled) { color: var(--c-primary); }
.link-btn:disabled { color: var(--c-text-muted); cursor: not-allowed; }
.link-sep { color: var(--c-border-strong); font-size: 12px; }

.field {
  margin-bottom: 14px;
}
.field label {
  display: block;
  font-size: 13px;
  color: var(--c-text-secondary);
  margin-bottom: 6px;
  font-weight: 500;
}
.field label .optional {
  color: var(--c-text-muted);
  font-weight: 400;
  font-size: 12px;
}
.field input {
  width: 100%;
  height: 46px;
  padding: 0 14px;
  border: 1px solid var(--c-border-strong);
  border-radius: var(--radius-sm);
  font-size: 15px;
  background: var(--c-surface);
  color: var(--c-text);
  outline: none;
  font-family: var(--font);
  transition: border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease);
}
.field input::placeholder { color: var(--c-text-muted); }
.field input:focus {
  border-color: var(--c-primary);
  box-shadow: 0 0 0 4px rgba(80, 104, 240, .12);
}

/* 完成页 */
.done-body { text-align: center; }
.done-icon { display: flex; justify-content: center; margin-bottom: 14px; }
.check-svg { width: 72px; height: 72px; }
.check-circle {
  stroke: var(--c-success);
  stroke-width: 2.5;
  stroke-dasharray: 151;
  stroke-dashoffset: 151;
  animation: dash .5s var(--ease) forwards;
}
.check-path {
  stroke: var(--c-success);
  stroke-width: 3.5;
  stroke-linecap: round;
  stroke-dasharray: 36;
  stroke-dashoffset: 36;
  animation: dash .35s .35s var(--ease) forwards;
}
@keyframes dash { to { stroke-dashoffset: 0; } }

.done-title { font-size: 18px; font-weight: 700; letter-spacing: -.01em; margin-bottom: 14px; }
.done-credits { margin-bottom: 8px; }
.credits-num {
  font-size: 40px; font-weight: 700; line-height: 1.1;
  letter-spacing: -.02em;
  font-variant-numeric: tabular-nums;
  background: var(--c-grad);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.credits-label { color: var(--c-text-muted); font-size: 13px; margin-top: 2px; }
.done-body .btn { margin-top: 18px; }

.reg-foot {
  text-align: center; margin-top: 22px; padding-top: 16px;
  border-top: 1px solid var(--c-border);
  font-size: 12px; color: var(--c-text-muted);
}
.muted { color: var(--c-text-muted); font-size: 13px; }
</style>
