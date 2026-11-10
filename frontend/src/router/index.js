import { createRouter, createWebHistory } from 'vue-router'
import Login from '../views/Login.vue'
import Home from '../views/Home.vue'
import Message from '../views/Message.vue'
import VideoPlay from '../views/VideoPlay.vue'
import Publish from '../views/Publish.vue'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: Login,
    meta: { public: true }
  },
  {
    path: '/',
    name: 'Home',
    component: Home
  },
  {
    path: '/video/:id',
    name: 'VideoPlay',
    component: VideoPlay
  },
  {
    path: '/message',
    name: 'Message',
    component: Message
  },
  {
    path: '/publish',
    name: 'Publish',
    component: Publish
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/'
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

// 路由守卫：未登录只能访问 public 页面
router.beforeEach((to) => {
  const token = localStorage.getItem('token')

  if (!to.meta.public && !token) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  // 已登录再访问登录页，直接回主页
  if (to.path === '/login' && token) {
    return { path: '/' }
  }
})

export default router
