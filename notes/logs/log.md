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

### 8.24
引入Redis黑名单机制，登出时拉黑该用户的access token
新增登出时先判断传入的refresh token是否属于该用户

### 8.29
refresh接口也增加了拉黑旧access token的机制，但是这个字段是非必需的
新增get profile接口，获取用户基础信息
为用户新增默认头像和简介，注册时直接填入默认头像和简介，后续在update profile接口里修改，在get profile接口里一并返回

### 8.30
新增update profile接口，允许用户修改头像和简介，并且预留avarta url字段的修改权限，用作前端上传头像后获得后端返回的url地址，前端再将该url地址传给update profile接口进行修改
新增upload avatar接口，允许用户上传头像，后端将头像保存到本地，并返回相对路径url给前端，前端再将该url地址传给update profile接口进行修改
新增change password接口，允许用户修改密码，同时在修改密码时将旧的access token拉黑，现阶段未完成删除该用户所有refresh token的功能，后续在redis里增加双向索引，方便删除该用户所有refresh token

### 8.31
新增redis双向索引<userId, refreshToken_set>，方便删除该用户所有refresh token
设置定时异步清理任务，每internal时间间清理一次redis中refresh_token_set中已经过期的refresh token，避免redis中refresh_token_set无限增长
