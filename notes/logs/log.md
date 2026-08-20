### 8.16

建项目文件夹，建本地仓库和远程仓库并用git连接成功。

### 8.17

搭建基本项目目录结构。

### 8.18
infra：              database、cache
utils middleware：   jwt、response、auth、cors
model dto：          user、video、user_refresh_token、DTO
完成以上三部分的开发

### 8.19
完成了程序入口的配置加载
docker一键启动mysql、redis、rabbitmq
增加了健康检查路由、注册和登录路由，添加了对refresh token的生成、存表和返回前端功能。

### 8.20
新增刷新token、登出两个接口
集成Redis到登陆、刷新token、登出接口里，缓存refresh token
优化数据库的字段命名和时区设置
增加全链路透传context
取消repo层查询工作的SQL语句硬编码
