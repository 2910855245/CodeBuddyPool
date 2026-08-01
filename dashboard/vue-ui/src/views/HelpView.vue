<script setup lang="ts">
// 使用说明页：把面板里没写清的细节集中说明给用户
const sections = [
  {
    title: '这是什么',
    items: [
      '一个「AI 账号共享号池」管理面板：收集多个 CodeBuddy 账号，接入编程工具统一调度。',
      '你的好友通过「分享注册页」用手机号注册账号，自动加入号池；你把面板生成的 API 密钥发给编程工具（Claude Code / Codex / Kimi Code / OpenCode / OpenClaw / 通义灵码 等国内外高热度工具），工具就能自动从号池调度可用账号完成调用。',
    ],
  },
  {
    title: '号池管理（首页）',
    items: [
      '总账号 / 可用 / 耗尽 / 失效 / 总积分 / 重复账号：实时统计，顶部卡片一目了然。',
      '「可用」= 积分大于 0；「耗尽」= 积分用完；「失效」= 令牌失效无法再登录，建议删除。',
      '「重复账号」标红的行是同一手机号被加入了多次，建议删除多余的只留一个。',
      '积分每 15 秒自动刷新一次（读本地缓存，不卡）；点行内「刷新」可立即拉取该号最新积分。',
      '「全部刷新」会逐个账号真实拉取上游积分，账号多时需等待（约每个号 1 秒），属正常现象。',
    ],
  },
  {
    title: '自动签到',
    items: [
      '系统每天凌晨自动批量签到，为所有账号领取每日积分，无需手动操作。',
      '新注册入池的账号会即时自动签到一次，立刻获得签到积分。',
    ],
  },
  {
    title: 'API 密钥',
    items: [
      '点「新建密钥」创建发给调用方的 Key，备注名用来区分发给谁。',
      '明文 Key 只在创建成功时显示一次，请立即复制保存，之后无法再次查看。',
      '可随时「禁用」或「删除」某个 Key，对方立即失效，不影响其他 Key。',
      '列表展示每个 Key 的请求数、成功数、Token 消耗、最近调用时间，每 4 秒自动刷新。',
    ],
  },
  {
    title: '怎么调用',
    items: [
      '对外是 OpenAI 兼容接口，地址见「部署文档」页的 proxy_base。',
      '调用方在请求头带 Authorization: Bearer 你的Key 即可，model 用面板支持的模型名。',
      '同时兼容 Anthropic（Claude）格式，具体见「部署文档」页。',
    ],
  },
  {
    title: '反风控机制（怎么避免触发上游风控）',
    items: [
      '智能轮换：优先挑「闲置超过 5 分钟」的账号使用，不让单个号连续高频请求；同档内按「历史成功率」加权随机，表现差的号自动降权。',
      '失败自动换号：一次调用失败会自动换其它账号重试，最多切换 3 个号，并按 1s / 2s / 4s 逐步拉长间隔，避免立刻猛打上游。',
      '单号限速：同一账号两次请求至少间隔 1 秒、同一时刻只允许 1 个并发，防止把单个号打爆触发风控。',
      '限流软冷却：账号命中上游「请求过于频繁」时进入 20 秒冷却自动恢复，不直接判死；连续失败 5 次也会短暂冷却，成功一次即恢复。',
      '积分耗尽保护：识别到「余额不足 / 配额超限」后标记耗尽，12 小时内不再调度、到期自动重试，避免反复无效请求。',
      '失效误判保护：只有「令牌失效 (401)」才把号判死；临时的 403 风控波动不杀号，防止大面积误掉号。',
      '账单轮询摊平：积分每 45 秒只查 1 个号、轮转覆盖全池，不集中打账单接口；这也是为什么面板读的是缓存、不会卡。',
      '签到错峰：每日凌晨批量签到，号与号之间间隔 30 秒拉长到数分钟，模拟真人分散操作而非机器并发。',
      '指纹伪装：所有请求统一携带桌面 Chrome 的 User-Agent，且批量操作（全部刷新 / 全部签到）有冷却和全局互斥，防止连点造成请求风暴。',
    ],
  },
  {
    title: '常见问题',
    items: [
      '请求日志为空：说明还没人用你的 Key 发起调用，或代理服务（9091）没在运行，去「代理」页确认在线。',
      '积分不更新：点对应账号的「刷新」强制拉一次；仍不更新多半是令牌失效，删除重加。',
      '忘记管理员密码：目前无法自助找回，需登录服务器修改（见部署文档）。',
      '密钥丢了：无法找回，只能删除后新建一个发给对方。',
    ],
  },
]
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div class="page-title-wrap">
        <h1 class="page-title">使用说明</h1>
        <p class="page-sub">面板功能与常见问题集中说明</p>
      </div>
    </header>

    <div v-for="(s, si) in sections" :key="s.title" class="card section" :style="{ animationDelay: `${si * 50}ms` }">
      <div class="sec-title">{{ s.title }}</div>
      <ul class="sec-list">
        <li v-for="(it, i) in s.items" :key="i">{{ it }}</li>
      </ul>
    </div>
  </div>
</template>


<style scoped>
.page { max-width: 860px; margin: 0 auto; animation: fadeInUp .32s var(--ease); }
.page-head { margin-bottom: 22px; }
.page-title-wrap { position: relative; padding-left: 14px; }
.page-title-wrap::before {
  content: '';
  position: absolute;
  left: 0; top: 5px; bottom: 5px;
  width: 4px;
  border-radius: 999px;
  background: var(--c-grad);
}
.page-title { font-size: 26px; font-weight: 600; letter-spacing: -.02em; margin: 0 0 3px; color: var(--c-text); }
.page-sub { color: var(--c-text-muted); margin: 0; font-size: 14px; }

.section { margin-bottom: 14px; animation: fadeInUp .45s var(--ease) backwards; }
.sec-title {
  font-size: 15.5px; font-weight: 600; margin-bottom: 12px;
  letter-spacing: -.01em;
  background: var(--c-grad);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  display: inline-block;
}
.sec-list { margin: 0; padding-left: 0; display: flex; flex-direction: column; gap: 9px; list-style: none; }
.sec-list li {
  position: relative;
  font-size: 13.5px; color: var(--c-text-secondary); line-height: 1.65;
  padding-left: 16px;
}
.sec-list li::before {
  content: '';
  position: absolute;
  left: 0; top: 9px;
  width: 5px; height: 5px;
  border-radius: 50%;
  background: var(--c-primary);
  opacity: .55;
}

@media (max-width: 768px) {
  .page-title { font-size: 22px; }
}
</style>
