import { createRouter, createWebHistory } from 'vue-router'
import Login from '../views/Login.vue'
import MainLayout from '../layouts/MainLayout.vue'
import Feed from '../views/Feed.vue'
import Message from '../views/Message.vue'
import Notification from '../views/Notification.vue'
import VideoPlay from '../views/VideoPlay.vue'
import Publish from '../views/Publish.vue'
import UserProfile from '../views/UserProfile.vue'
import Search from '../views/Search.vue'

// 除登录页外，所有页面都挂在 MainLayout 下，保证顶部导航栏与侧边栏始终存在。
// meta.immersive 表示主内容区使用 100vh 沉浸式布局（推荐流 / 关注流）。
const routes = [
  {
    path: '/login',
    name: 'Login',
    component: Login,
    meta: { public: true }
  },
  {
    path: '/',
    component: MainLayout,
    children: [
      {
        path: '',
        name: 'Home',
        component: Feed,
        props: { mode: 'recommend' },
        meta: { immersive: true }
      },
      {
        path: 'following',
        name: 'Following',
        component: Feed,
        props: { mode: 'following' },
        meta: { immersive: true }
      },
      {
        path: 'search',
        name: 'Search',
        component: Search
      },
      {
        path: 'video/:id',
        name: 'VideoPlay',
        component: VideoPlay
      },
      {
        path: 'message',
        name: 'Message',
        component: Message
      },
      {
        path: 'notification',
        name: 'Notification',
        component: Notification
      },
      {
        path: 'publish',
        name: 'Publish',
        component: Publish
      },
      {
        path: 'user/:id',
        name: 'UserProfile',
        component: UserProfile
      }
    ]
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
