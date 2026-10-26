import tseslint from 'typescript-eslint'

// 从简口径：只启用 typescript-eslint 推荐集，覆盖 .ts/.tsx；构建产物与
// 依赖目录不参与。按需再补 react-hooks 等专项插件。
export default tseslint.config(
  { ignores: ['dist/'] },
  ...tseslint.configs.recommended,
)
