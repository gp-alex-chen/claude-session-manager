export function isWorking(agent) {
  return agent && (agent.state === 'working' || agent.status === 'busy');
}
