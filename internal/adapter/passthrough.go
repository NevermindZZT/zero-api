package adapter

// CanPassthrough 判断请求是否可以按原协议透传到上游。
// 渠道协议列表是能力边界；模型声明支持当前下游协议时，
// 只有渠道也支持该协议才允许透传。
func CanPassthrough(channelType, downstreamProtocol string, channelSupportsProtocol, modelSupportsProtocol bool) bool {
	if channelType == downstreamProtocol {
		return true
	}
	return channelSupportsProtocol && modelSupportsProtocol
}
