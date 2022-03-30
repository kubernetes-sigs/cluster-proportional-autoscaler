### Version 1.8.5 (Wed Aug 14 2021 Zihong Zheng <zihongz@google.com>)
 - Change minimum replicas to zero in laddermode.

### Version 1.8.4 (Thu June 29 2021 Zihong Zheng <zihongz@google.com>)
 - Remove USER in Dockerfile.

### Version 1.8.3 (Fri Aug 14 2020 Zihong Zheng <zihongz@google.com>)
 - Fix the architecture metadata in the multi-arch imagee.

### Version 1.8.2 (Mon Aug 03 2020 Zihong Zheng <zihongz@google.com>)
 - Create a multiarch image.

### Version 1.8.1 (Fri June 12 2020 Zihong Zheng <zihongz@google.com>)
 - fix core calculate.

### Version 1.8.0 (Thu May 21 2020 Zihong Zheng <zihongz@google.com>)
 - Allow ladder to support setting replicas to 0.
 - Add simple healthz.
 - use node.Status.Allocatable when calculate cores.
 - Add includeUnschedulableNodes option

### Version 1.7.1 (Tue Aug 27 2019 Zihong Zheng <zihongz@google.com>)
 - Fix a bug that controller is blocked by reflector.Run().

### Version 1.7.0 (Sat Aug 17 2019 Zihong Zheng <zihongz@google.com>)
 - Update to use client-go@kubernetes-1.15.1.

### Version 1.6.0 (Thu May 02 2019 Zihong Zheng <zihongz@google.com>)
 - Rebase base image to distroless.

### Version 1.5.0 (Fri Mar 29 2019 Zihong Zheng <zihongz@google.com>)
 - Add support to filter nodes by node labels.

### Version 1.4.0 (Tue Jan 29 2019 Zihong Zheng <zihongz@google.com>)
 - Add support for scaling via apps/v1 and v1 APIs.

### Version 1.3.0 (Tue Oct 02 2018 Zihong Zheng <zihongz@google.com>)
 - Rebase docker image on scratch..

### Version 1.2.0 (Wed July 11 2018 Zihong Zheng <zihongz@google.com>)
 - Watch nodes instead of periodic polls.

### Version 1.1.2-r2 (Mon June 12 2017 Zihong Zheng <zihongz@google.com>)
 - Update base image and rebuild.

### Version 1.1.2 (Thu June 1 2017 Zihong Zheng <zihongz@google.com>)
 - Update client-go to 3.0 beta.

### Version 1.1.1 (Thu February 23 2017 Zihong Zheng <zihongz@google.com>)
 - Use protobufs for communication with apiserver.

### Version 1.1.0 (Wed February 22 2017 Zihong Zheng <zihongz@google.com>)
 - Adds 'preventSinglePointFailure' option to linear controller and supports
   switching control mode on-the-fly.

### Version 1.0.0 (Mon November 7 2016 Zihong Zheng <zihongz@google.com>)
 - Releases autoscaler 1.0.0 with linear controller and default params support.
