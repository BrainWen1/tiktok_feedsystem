<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '../utils/request'

const router = useRouter()
const route = useRoute()

const formRef = ref()
const mode = ref('login') // login | register
const loading = ref(false)

const form = reactive({
  user_name: '',
  password: '',
  confirmPassword: ''
})

const isRegister = computed(() => mode.value === 'register')

const rules = computed(() => {
  const base = {
    user_name: [
      { required: true, message: '请输入用户名', trigger: 'blur' },
      ...(isRegister.value
        ? [
            { min: 3, max: 32, message: '用户名长度需为 3-32 位', trigger: 'blur' },
            {
              pattern: /^[A-Za-z0-9_]+$/,
              message: '用户名只能包含字母、数字和下划线',
              trigger: 'blur'
            }
          ]
        : [])
    ],
    password: [
      { required: true, message: '请输入密码', trigger: 'blur' },
      ...(isRegister.value
        ? [
            { min: 8, message: '密码长度不能少于 8 位', trigger: 'blur' },
            {
              pattern: /^(?=.*[A-Za-z])(?=.*\d).+$/,
              message: '密码需同时包含字母和数字',
              trigger: 'blur'
            }
          ]
        : [])
    ]
  }

  if (isRegister.value) {
    base.confirmPassword = [
      { required: true, message: '请再次输入密码', trigger: 'blur' },
      {
        validator: (_rule, value, callback) => {
          if (value !== form.password) {
            callback(new Error('两次输入的密码不一致'))
          } else {
            callback()
          }
        },
        trigger: 'blur'
      }
    ]
  }

  return base
})

// 切换登录/注册时清空校验状态
watch(mode, () => {
  formRef.value?.clearValidate()
})

async function handleSubmit() {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    if (isRegister.value) {
      await request.post('/user/register', {
        user_name: form.user_name,
        password: form.password
      })
      ElMessage.success('注册成功，请登录')
      mode.value = 'login'
      form.password = ''
      form.confirmPassword = ''
    } else {
      const res = await request.post('/user/login', {
        user_name: form.user_name,
        password: form.password
      })

      const data = res.data || {}
      localStorage.setItem('token', data.token)
      if (data.refresh_token) {
        localStorage.setItem('refresh_token', data.refresh_token)
      }
      localStorage.setItem('user_name', data.user_name || form.user_name)

      ElMessage.success('登录成功')
      const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
      router.replace(redirect)
    }
  } catch {
    // 错误提示已由 request.js 响应拦截器统一处理
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <el-card class="login-card" shadow="always">
      <h2 class="title">TikTok Feed 演示</h2>

      <el-tabs v-model="mode" stretch>
        <el-tab-pane label="登录" name="login" />
        <el-tab-pane label="注册" name="register" />
      </el-tabs>

      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent>
        <el-form-item label="用户名" prop="user_name">
          <el-input
            v-model="form.user_name"
            placeholder="请输入用户名"
            clearable
            autocomplete="username"
          />
        </el-form-item>

        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="请输入密码"
            show-password
            :autocomplete="isRegister ? 'new-password' : 'current-password'"
            @keyup.enter="handleSubmit"
          />
        </el-form-item>

        <el-form-item v-if="isRegister" label="确认密码" prop="confirmPassword">
          <el-input
            v-model="form.confirmPassword"
            type="password"
            placeholder="请再次输入密码"
            show-password
            autocomplete="new-password"
            @keyup.enter="handleSubmit"
          />
        </el-form-item>

        <el-button
          class="submit-btn"
          type="primary"
          size="large"
          :loading="loading"
          @click="handleSubmit"
        >
          {{ isRegister ? '注册' : '登录' }}
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: flex-start;
  justify-content: center;
  min-height: 100%;
  padding-top: 10vh;
}

.login-card {
  width: 400px;
}

.title {
  margin: 0 0 16px;
  text-align: center;
  color: #333;
}

.submit-btn {
  width: 100%;
  margin-top: 8px;
}
</style>
