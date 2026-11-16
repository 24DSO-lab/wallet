import eslint from '@eslint/js'
import tseslint from 'typescript-eslint'
import vue from 'eslint-plugin-vue'
export default tseslint.config({ignores:['dist/**','wailsjs/**']},eslint.configs.recommended,...tseslint.configs.recommended,...vue.configs['flat/recommended'],{files:['**/*.vue'],languageOptions:{parserOptions:{parser:tseslint.parser}}},{files:['**/*.ts','**/*.vue'],rules:{'no-restricted-globals':['error','parseFloat','Number'],'vue/multi-word-component-names':'off','@typescript-eslint/no-explicit-any':'error'}})