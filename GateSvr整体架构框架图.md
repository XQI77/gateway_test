# GateSvr 整体架构框架图

基于对整个项目的深入分析，绘制包含Session管理、连接协程、异步处理协程池的完整架构框架图。

## 1. 系统整体架构图

```mermaid
graph TB
    subgraph CLIENTS["客户端层"]
        C1[QUIC客户端-1]
        C2[QUIC客户端-2]
        C3[QUIC客户端-3]
        CN[QUIC客户端-N]
    end
    
    subgraph GATESVR["GateSvr 网关服务器"]
        subgraph QUIC_LAYER["QUIC 接入层"]
            QS[QUIC Server<br/>监听:8453]
            CL[Connection Listener<br/>连接监听器]
        end
        
        subgraph CONN_POOL["连接协程池"]
            CH1[连接协程-1<br/>处理Client-1]
            CH2[连接协程-2<br/>处理Client-2] 
            CH3[连接协程-3<br/>处理Client-3]
            CHN[连接协程-N<br/>处理Client-N]
        end
        
        subgraph SESSION_MGR["Session管理层"]
            SM[SessionManager<br/>会话管理器]
            
            subgraph SESSIONS["Session实例集合"]
                S1[Session-1<br/>含有序队列]
                S2[Session-2<br/>含有序队列]
                S3[Session-3<br/>含有序队列]
                SN[Session-N<br/>含有序队列]
            end
            
            subgraph QUEUES["有序队列集合"]
                Q1[OrderedQueue-1<br/>等待队列+已发送队列]
                Q2[OrderedQueue-2<br/>等待队列+已发送队列]
                Q3[OrderedQueue-3<br/>等待队列+已发送队列]
                QN[OrderedQueue-N<br/>等待队列+已发送队列]
            end
        end
        
        subgraph ASYNC_LAYER["异步处理层"]
            AP[AsyncProcessor<br/>异步处理器]
            
            subgraph WORKER_POOL["Worker协程池"]
                W1[Worker-1<br/>处理异步任务]
                W2[Worker-2<br/>处理异步任务]
                W3[Worker-3<br/>处理异步任务]
                WN[Worker-N<br/>CPU核数x4]
            end
            
            TQ[TaskQueue<br/>任务队列<br/>容量:10000]
        end
        
        subgraph MESSAGE_LAYER["消息处理层"]
            OMS[OrderedSender<br/>有序发送器]
            MC[MessageCodec<br/>消息编解码器]
            NOM[NotifyOrderingManager<br/>通知保序管理器]
        end
        
        subgraph UPSTREAM_LAYER["上游服务层"]
            UR[UpstreamRouter<br/>服务路由器]
            UM[UpstreamManager<br/>连接管理器]
        end
    end
    
    subgraph UPSTREAM["上游服务集群"]
        US1[Hello Service<br/>登录认证<br/>:8081]
        US2[Business Service<br/>业务处理<br/>:8082]
        US3[Zone Service<br/>区域功能<br/>:8083]
    end
    
    %% 客户端连接
    C1 -.->|QUIC/TLS连接| QS
    C2 -.->|QUIC/TLS连接| QS
    C3 -.->|QUIC/TLS连接| QS
    CN -.->|QUIC/TLS连接| QS
    
    %% QUIC层到连接协程
    QS --> CL
    CL --> CH1
    CL --> CH2
    CL --> CH3
    CL --> CHN
    
    %% 连接协程到Session
    CH1 <--> S1
    CH2 <--> S2
    CH3 <--> S3
    CHN <--> SN
    
    %% Session管理
    SM --> S1
    SM --> S2
    SM --> S3
    SM --> SN
    
    %% Session到有序队列
    S1 <--> Q1
    S2 <--> Q2
    S3 <--> Q3
    SN <--> QN
    
    %% 连接协程到异步处理
    CH1 --> TQ
    CH2 --> TQ
    CH3 --> TQ
    CHN --> TQ
    
    %% 异步处理流转
    TQ --> AP
    AP --> W1
    AP --> W2
    AP --> W3
    AP --> WN
    
    %% Worker到消息处理
    W1 --> OMS
    W2 --> OMS
    W3 --> OMS
    WN --> OMS
    
    %% 消息处理层关系
    OMS --> MC
    OMS --> NOM
    OMS --> Q1
    OMS --> Q2
    OMS --> Q3
    OMS --> QN
    
    %% 异步处理到上游
    W1 --> UR
    W2 --> UR
    W3 --> UR
    WN --> UR
    
    %% 上游路由
    UR --> UM
    UM -.->|gRPC| US1
    UM -.->|gRPC| US2
    UM -.->|gRPC| US3
    
    %% 样式定义
    style QS fill:#e1f5fe
    style CH1 fill:#f3e5f5
    style CH2 fill:#f3e5f5
    style CH3 fill:#f3e5f5
    style CHN fill:#f3e5f5
    style SM fill:#e8f5e8
    style S1 fill:#fff3e0
    style S2 fill:#fff3e0
    style S3 fill:#fff3e0
    style SN fill:#fff3e0
    style AP fill:#ffebee
    style W1 fill:#f1f8e9
    style W2 fill:#f1f8e9
    style W3 fill:#f1f8e9
    style WN fill:#f1f8e9
    style TQ fill:#fce4ec
    style OMS fill:#e3f2fd
```

## 2. 连接协程处理流程图

```mermaid
sequenceDiagram
    participant C as QUIC客户端
    participant CH as 连接协程
    participant S as Session对象
    participant Q as OrderedQueue
    participant TQ as 任务队列
    participant AP as AsyncProcessor
    participant W as Worker协程
    participant US as 上游服务
    
    Note over C,US: 1. 连接建立与Session创建
    C->>+CH: QUIC连接建立
    CH->>+S: CreateSession()
    S->>+Q: NewOrderedMessageQueue()
    Q-->>-S: 队列初始化完成
    S-->>-CH: Session创建完成
    CH-->>-C: 连接建立成功
    
    Note over C,US: 2. 业务请求处理
    C->>+CH: BusinessRequest(SeqId=123)
    CH->>+S: 更新Session状态
    S->>S: UpdateLastActivity()
    S->>S: UpdateMaxClientSeq(123)
    S-->>-CH: Session更新完成
    
    Note over C,US: 3. 同步vs异步决策
    CH->>CH: 判断请求类型
    
    alt Hello请求(同步处理)
        CH->>US: 直接调用上游服务
        US-->>CH: 返回登录结果
        CH->>+S: 更新登录状态
        S-->>-CH: 状态更新完成
        CH->>Q: 发送响应消息
        Q-->>C: 有序响应
    else 业务请求(异步处理)
        CH->>CH: 创建AsyncTask
        CH->>+TQ: 提交异步任务
        TQ-->>-CH: 提交成功(非阻塞)
        CH-->>C: 请求已接收
        
        Note over TQ,US: 4. 异步Worker处理
        TQ->>+AP: 分发任务
        AP->>+W: 分配给Worker
        W->>W: 检查Session有效性
        W->>US: 调用上游服务
        US-->>W: 返回业务结果
        W->>Q: 通过有序队列发送响应
        Q-->>C: 有序响应
        W-->>-AP: 任务完成
        AP-->>-TQ: 处理完成
    end
```

## 3. Session与有序队列详细结构图

```mermaid
graph TB
    subgraph SESSION["Session对象结构"]
        subgraph SESSION_BASIC["基础信息"]
            SID[SessionID<br/>会话唯一标识]
            CONN[QUIC Connection<br/>底层连接]
            STREAM[QUIC Stream<br/>双向流]
            STATE[SessionState<br/>Inited/Normal/Closed]
        end
        
        subgraph SESSION_CLIENT["客户端信息"]
            CID[ClientID<br/>客户端标识]
            OID[OpenID<br/>用户标识]
            ZONE[Zone<br/>分区信息]
            IP[UserIP<br/>客户端IP]
        end
        
        subgraph SESSION_SEQ["序列号管理"]
            NSS[NextServerSeq<br/>下个服务器序列号]
            MCS[MaxClientSeq<br/>最大客户端序列号]
            CAS[ClientAckServerSeq<br/>客户端确认序列号]
        end
        
        subgraph SESSION_TIME["时间管理"]
            CT[CreateTime<br/>创建时间]
            LA[LastActivity<br/>最后活跃时间]
        end
    end
    
    subgraph ORDERED_QUEUE["有序消息队列"]
        subgraph QUEUE_BASIC["队列基础"]
            QID[SessionID<br/>所属会话ID]
            MAX_SIZE[MaxQueueSize<br/>最大队列容量]
            CALLBACK[SendCallback<br/>发送回调函数]
        end
        
        subgraph WAITING_QUEUE["等待队列(最小堆)"]
            WQ[WaitingQueue<br/>MessageQueue]
            WQ_DESC[存储等待发送的消息<br/>按序列号排序<br/>处理序列号跳跃]
        end
        
        subgraph SENT_QUEUE["已发送队列(Map)"]
            SM[SentMessages<br/>map[uint64]*OrderedMessage]
            SM_DESC[存储已发送待确认消息<br/>支持重传和批量ACK<br/>客户端确认后清理]
        end
        
        subgraph QUEUE_SEQ["队列序列号"]
            NES[NextExpectedSeq<br/>下个期望序列号]
            LSS[LastSentSeq<br/>最后发送序列号]
            LAS[LastAckedSeq<br/>最后确认序列号]
        end
        
        subgraph QUEUE_CONTROL["队列控制"]
            STOPPED[Stopped<br/>停止标志]
            STOP_CH[StopChannel<br/>停止信号]
            CLEANUP[CleanupTicker<br/>清理定时器]
        end
    end
    
    subgraph NOTIFY_MANAGER["通知保序管理器"]
        subgraph BEFORE_NOTIFY["Response前通知"]
            BN[BeforeNotifies<br/>map[uint32][]*NotifyItem]
            BN_DESC[按客户端序列号绑定<br/>在业务响应前发送]
        end
        
        subgraph AFTER_NOTIFY["Response后通知"]
            AN[AfterNotifies<br/>map[uint32][]*NotifyItem]
            AN_DESC[按客户端序列号绑定<br/>在业务响应后发送]
        end
    end
    
    %% Session内部关系
    SID --> CONN
    CONN --> STREAM
    OID --> ZONE
    NSS --> MCS
    MCS --> CAS
    
    %% Session到队列
    SESSION --> ORDERED_QUEUE
    SID -.-> QID
    NSS -.-> NES
    
    %% 队列内部关系
    WQ --> WQ_DESC
    SM --> SM_DESC
    NES --> LSS
    LSS --> LAS
    CALLBACK --> SM
    CLEANUP --> SM
    
    %% Session到通知管理器
    SESSION --> NOTIFY_MANAGER
    MCS -.-> BN
    MCS -.-> AN
    
    %% 样式定义
    style SESSION fill:#e1f5fe
    style SESSION_BASIC fill:#f3e5f5
    style SESSION_CLIENT fill:#e8f5e8
    style SESSION_SEQ fill:#fff3e0
    style SESSION_TIME fill:#ffebee
    style ORDERED_QUEUE fill:#f1f8e9
    style WAITING_QUEUE fill:#e3f2fd
    style SENT_QUEUE fill:#fce4ec
    style NOTIFY_MANAGER fill:#f9fbe7
```

## 4. 异步处理协程池架构图

```mermaid
graph TB
    subgraph ASYNC_PROCESSOR["AsyncRequestProcessor 异步处理器"]
        subgraph CONFIG["配置参数"]
            MW[MaxWorkers<br/>最大协程数<br/>默认CPU核数x4]
            MQS[MaxQueueSize<br/>队列容量<br/>默认10000]
            TTO[TaskTimeout<br/>任务超时<br/>默认30秒]
        end
        
        subgraph CONTROL["控制组件"]
            STARTED[Started<br/>启动状态]
            STOP_CH[StopChannel<br/>停止信号]
            WG[WaitGroup<br/>协程同步]
            MUTEX[StartMutex<br/>启动锁]
        end
        
        TASK_QUEUE[TaskQueue<br/>任务队列<br/>chan *AsyncTask]
    end
    
    subgraph WORKER_POOL["Worker协程池"]
        W1[Worker-1<br/>协程ID: 1]
        W2[Worker-2<br/>协程ID: 2]
        W3[Worker-3<br/>协程ID: 3]
        W4[Worker-4<br/>协程ID: 4]
        WN[Worker-N<br/>协程ID: MaxWorkers]
    end
    
    subgraph TASK_PROCESSING["任务处理流程"]
        subgraph TASK_STRUCT["AsyncTask结构"]
            TID[TaskID<br/>任务标识]
            SID[SessionID<br/>会话标识]
            SESS[Session<br/>会话对象引用]
            REQ[Request<br/>客户端请求]
            BREQ[BusinessReq<br/>业务请求]
            ST[ServiceType<br/>服务类型]
            UREQ[UpstreamReq<br/>上游请求]
            CTX[Context<br/>超时上下文]
            LOGIN[IsLogin<br/>登录标志]
        end
        
        subgraph PROCESS_STEPS["处理步骤"]
            SV[SessionValid<br/>会话有效性检查]
            UCS[CallUpstreamService<br/>调用上游服务]
            BN[SendBeforeNotifies<br/>发送前置通知]
            BR[SendBusinessResponse<br/>发送业务响应]
            AN[SendAfterNotifies<br/>发送后置通知]
        end
    end
    
    subgraph UPSTREAM_SERVICES["上游服务集群"]
        HELLO[Hello Service<br/>登录认证服务<br/>端口:8081]
        BUSINESS[Business Service<br/>业务处理服务<br/>端口:8082]
        ZONE[Zone Service<br/>区域功能服务<br/>端口:8083]
    end
    
    %% 配置关系
    MW --> WORKER_POOL
    MQS --> TASK_QUEUE
    TTO --> CTX
    
    %% 控制关系
    STARTED --> W1
    STARTED --> W2
    STARTED --> W3
    STARTED --> W4
    STARTED --> WN
    STOP_CH --> W1
    STOP_CH --> W2
    STOP_CH --> W3
    STOP_CH --> W4
    STOP_CH --> WN
    
    %% 任务流转
    TASK_QUEUE --> W1
    TASK_QUEUE --> W2
    TASK_QUEUE --> W3
    TASK_QUEUE --> W4
    TASK_QUEUE --> WN
    
    %% Worker处理流程
    W1 --> SV
    W2 --> SV
    W3 --> SV
    W4 --> SV
    WN --> SV
    
    SV --> UCS
    UCS --> BN
    BN --> BR
    BR --> AN
    
    %% 上游服务调用
    UCS --> HELLO
    UCS --> BUSINESS
    UCS --> ZONE
    
    %% 任务结构关系
    TID --> SESS
    SID --> SESS
    REQ --> BREQ
    BREQ --> UREQ
    ST --> UREQ
    
    %% 样式定义
    style ASYNC_PROCESSOR fill:#e1f5fe
    style CONFIG fill:#f3e5f5
    style CONTROL fill:#e8f5e8
    style TASK_QUEUE fill:#fff3e0
    style WORKER_POOL fill:#ffebee
    style W1 fill:#f1f8e9
    style W2 fill:#f1f8e9
    style W3 fill:#f1f8e9
    style W4 fill:#f1f8e9
    style WN fill:#f1f8e9
    style TASK_PROCESSING fill:#e3f2fd
    style UPSTREAM_SERVICES fill:#fce4ec
```

## 5. 系统核心交互流程图

```mermaid
sequenceDiagram
    participant C as QUIC客户端
    participant CH as 连接协程
    participant SM as SessionManager
    participant S as Session
    participant Q as OrderedQueue
    participant AP as AsyncProcessor
    participant W as Worker协程
    participant US as 上游服务
    participant OMS as OrderedSender
    
    Note over C,OMS: 系统启动阶段
    CH->>SM: 初始化SessionManager
    CH->>AP: 启动AsyncProcessor
    AP->>W: 启动Worker协程池
    
    Note over C,OMS: 客户端连接阶段
    C->>+CH: QUIC连接请求
    CH->>+SM: CreateSession(conn, stream, clientID, openID)
    SM->>+S: 创建Session实例
    S->>+Q: NewOrderedMessageQueue(sessionID, maxSize)
    Q->>Q: 初始化等待队列和已发送队列
    Q->>Q: 启动清理协程
    Q-->>-S: 队列创建完成
    S->>S: 设置Session基础信息
    S-->>-SM: Session创建完成
    SM->>SM: 添加到sessions索引
    SM-->>-CH: 返回Session对象
    CH-->>-C: 连接建立成功
    
    Note over C,OMS: 业务请求处理阶段
    C->>+CH: BusinessRequest(SeqId=100, Action="getData")
    CH->>+S: 更新Session状态
    S->>S: atomic.StoreUint64(&lastActivity, now)
    S->>S: atomic.StoreUint64(&maxClientSeq, 100)
    S-->>-CH: Session状态更新完成
    
    CH->>CH: determineUpstreamService(action)
    CH->>CH: 判断是否为Hello请求
    
    alt 非Hello请求(异步处理)
        CH->>CH: 创建AsyncTask{TaskID, Session, Request}
        CH->>+AP: SubmitTask(asyncTask)
        
        alt 任务队列未满
            AP->>AP: taskQueue <- asyncTask
            AP-->>-CH: 提交成功(true)
            CH-->>-C: 请求已接收(非阻塞返回)
            
            Note over W,OMS: Worker异步处理
            W->>+AP: 从队列获取任务
            AP->>AP: task := <-taskQueue
            AP-->>-W: 返回AsyncTask
            
            W->>+S: isSessionValid()检查
            S->>S: 检查state != SessionClosed
            S->>S: 检查connection != nil
            S-->>-W: 会话状态有效
            
            W->>+US: callUpstreamService(ctx, serviceType, upstreamReq)
            US->>US: 处理业务逻辑
            US-->>-W: UpstreamResponse{Code, Message, Data}
            
            Note over W,OMS: 有序响应发送
            W->>W: grid := uint32(task.Request.SeqId)
            W->>S: sendBeforeNotifies(session, grid)
            
            W->>+OMS: SendBusinessResponse(session, msgId, code, message, data)
            OMS->>+S: sess.NewServerSeq()获取新序列号
            S->>S: atomic.AddUint64(&nextSeqID, 1)
            S-->>-OMS: 返回serverSeq=201
            
            OMS->>OMS: push.SeqId = 201
            OMS->>OMS: messageCodec.EncodeServerPush(push)
            OMS->>+Q: EnqueueMessage(serverSeq=201, push, data)
            
            Q->>Q: 检查201 == nextExpectedSeq(201)
            Q->>Q: sendMessageDirectly(orderedMsg)
            Q->>Q: sentMessages[201] = orderedMsg
            Q->>Q: nextExpectedSeq = 202, lastSentSeq = 201
            Q->>Q: processWaitingMessages()处理等待队列
            Q-->>-OMS: 消息发送完成
            OMS-->>-W: 响应发送完成
            
            W->>S: sendAfterNotifies(session, grid)
            
        else 任务队列已满
            AP-->>CH: 提交失败(false)
            CH->>OMS: SendErrorResponse(503, "服务繁忙")
            OMS-->>C: ErrorResponse
        end
        
    else Hello请求(同步处理)
        CH->>+US: 直接调用Hello服务
        US-->>-CH: 登录结果
        CH->>+S: 更新登录状态和Zone信息
        S->>S: SetState(SessionNormal)
        S->>S: SetZone(zoneID)
        S-->>-CH: 状态更新完成
        CH->>OMS: 发送登录响应
        OMS-->>C: LoginResponse
    end
    
    Note over C,OMS: 客户端确认阶段
    C->>+CH: ClientAck(ackSeqId=201)
    CH->>+Q: AckMessagesUpTo(201)
    Q->>Q: 删除sentMessages中序列号<=201的消息
    Q->>Q: lastAckedSeq = 201
    Q-->>-CH: 确认完成，清理1条消息
    CH-->>-C: ACK处理完成
```

## 6. 架构特点总结

### 6.1 核心设计特点

1. **一对一连接协程模式**
   - 每个QUIC客户端对应一个专用的连接协程
   - 连接协程负责该客户端的所有消息接收和Session维护
   - 避免多连接间的竞争和锁冲突

2. **Session与有序队列绑定**
   - 每个Session包含一个独立的OrderedMessageQueue
   - 队列保证该Session内消息的严格有序性
   - 支持消息重传、批量ACK和超时清理

3. **异步处理协程池**
   - 连接协程快速处理Session更新后立即返回
   - 耗时的上游服务调用交给Worker协程池异步处理
   - 协程池大小可配置，默认CPU核数x4

4. **分层解耦架构**
   - 连接层：处理QUIC连接和基础协议
   - Session层：管理会话状态和消息队列
   - 异步层：处理业务逻辑和上游调用
   - 消息层：保证消息有序性和可靠传输

### 6.2 性能优势

- ✅ **高并发支持**: 连接协程非阻塞，支持大量并发连接
- ✅ **消息保序**: 每Session独立队列确保消息严格有序
- ✅ **弹性处理**: 异步协程池支持动态负载调整
- ✅ **容错能力**: 多层检查和重传机制保证可靠性
- ✅ **内存高效**: 基于堆的等待队列和Map的已发送队列
- ✅ **监控完善**: 完整的统计信息和性能指标

这种架构设计有效解决了高并发网关服务中的性能瓶颈和可靠性问题，实现了高吞吐量、低延迟的消息处理能力。