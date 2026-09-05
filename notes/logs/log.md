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

### 9.1
新增用户模块的find user by id/username接口，方便后续在视频模块中获取视频作者的用户信息
开始开发视频模块，新增上传视频和封面接口，以及发布视频接口，目前暂未做发布视频时传入的url的校验工作，后续会在redis中临时寄存上传记录
新增按id查询视频接口，带redis缓存热点数据，并且在缓存未命中时加分布式锁防止缓存击穿

### 9.2
新增按作者id查询视频接口
开始开发视频模块的like功能，新增like、unlike和is_liked接口。
    引入MQ削峰，like和unlike接口不直接操作数据库，而是将like事件发送到MQ中，由消费者异步处理，避免高并发下数据库压力过大；
    在MQ中使用幂等性和Redis缓存消息ID，避免重复点赞和网络抖动导致的重复消费问题，提升模块的稳定性和性能。

### 9.5
在worker的消费者里成功消费后，在redis里额外维护一个<userId, videoId_set>的键值对，便于查询用户是否点赞过某个视频，避免每次都去数据库查询，提升性能。只是还没有处理缓存雪崩问题，后续会在redis里增加空缓存占位，DB确认未点赞，也在Redis标记一下，避免反复穿透。
