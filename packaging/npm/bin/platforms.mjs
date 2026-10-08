export const targets = [
  'darwin-amd64', 'darwin-arm64',
  'linux-amd64', 'linux-arm64', 'linux-riscv64',
  'windows-amd64', 'windows-arm64',
];

export function platformDirectory(platform, arch) {
  const target = `${platform === 'win32' ? 'windows' : platform}-${arch === 'x64' ? 'amd64' : arch}`;
  return targets.includes(target) ? target : undefined;
}

export function binaryName(target) {
  return target.startsWith('windows-') ? 'tiana.exe' : 'tiana';
}

export function platformPackage(platform, arch) {
  return platformDirectory(platform, arch) ? `@tianacloud/cli-${platform}-${arch}` : undefined;
}
